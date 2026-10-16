# 技术设计方案：感知记录（抓拍/识别）与告警持久化存储

> 任务：`10-16-perception-records-and-alarms`
> 模块：`native` (ABI & Runner) + `internal/record` + `internal/database`

---

## 1. 架构全景与数据流转拓扑

```txt
┌────────────────────────────────────────────────────────────────────────┐
│                        Native C++ Runner 进程                          │
│  FrameDispatchMsg 携带帧捕获时间 capture_time_ms 与 pts                  │
│  api.process(task, &view, &result)                                     │
│  └── 向量检索命中 FaceTemplate (face_id, person_id, name, score)        │
│  └── 序列化 MSG_RESULT JSON (携带 capture_time_ms, matched_face_id 等)  │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ IPC UDS (MSG_RESULT)
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        Zhulong Go 宿主进程                             │
│                                                                        │
│  AIResultSubscription (Drop-Oldest 丢最旧环形队列背压)                   │
│         │                                                              │
│         ▼                                                              │
│  camera.StreamHub (分发推理结果给已注册的消费者)                          │
│         │                                                              │
│         ├─► camera.EventHub (实时 SSE: /api/v1/cameras/events 广播画框)  │
│         │                                                              │
│         └─► record.Service (持久化沉淀与证据管理服务)                   │
│                 │                                                      │
│                 ├─► 抓拍帧时间锚定 (captured_at = payload.capture_time) │
│                 │     (物理帧捕获时刻，彻底消灭推理/排队延迟漂移)      │
│                 │                                                      │
│                 ├─► 目标追踪与防抖削峰 (Tracking Cooldown)             │
│                 │     (同一目标在 3s 窗口内仅记录一次，防 SQLite 锁冲突)│
│                 │                                                      │
│                 ├─► 现场大图与特写抠图落盘                              │
│                 │     (写入 snapshots/{cam}/{date}/{id}.jpg)           │
│                 │                                                      │
│                 ├─► 底库快照对账 (Gallery Snapshot Resolution)         │
│                 │     (通过 matched_face_id 提取 person_faces 相对路径) │
│                 │                                                      │
│                 ▼                                                      │
│          GORM Store 批量短事务落库                                      │
│                 │                                                      │
│                 ├─► recognitions 表 (人脸/人员双图比对流水，含 Top-5)    │
│                 ├─► alarms 表 (违规目标告警，含全景大图与特写，免杀保护) │
│                 └─► captures 表 (过人过车卡口视觉抓拍，含结构化属性)    │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ HTTP RESTful API (internal/httputil 信封)
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        前端 / 外部集成系统                             │
│  • GET /api/v1/records/recognitions (双图核验卡片 & Top-5 名单)        │
│  • GET /api/v1/records/alarms       (告警研判中心 & 现场大图/特写)      │
│  • GET /api/v1/records/captures     (卡口以图搜图 & 结构化属性检索)    │
│  • GET /api/v1/media/content/{path} (直接读取现场抓拍/特写/底库原图)    │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 方案 B：Native C ABI、IPC 协议与 Runner 改造

### 2.1 IPC 协议扩充：帧采集时间戳透传 (`native/src/algorithm/ipc/ipc_protocol.hpp`)

为了保证事件时间精确等于**「摄像头抓拍的那一帧物理时间」**，在 `FrameDispatchMsg` 中增加 `capture_time_ms`：

```cpp
struct FrameDispatchMsg {
    uint64_t stream_id{0};       /**< 8 字节：物理流全局唯一 ID */
    uint64_t frame_id{0};        /**< 8 字节：视频帧全局单调自增序号 */
    uint64_t generation{0};      /**< 8 字节：槽位写入代次（8 字节对齐） */
    int64_t  pts{0};             /**< 8 字节：帧呈现时间戳（源流时间基） */
    int64_t  capture_time_ms{0}; /**< 8 字节：★ 帧物理采集/抓拍时刻的绝对 UTC 毫秒时间戳 */
    uint32_t slot_index{0};      /**< 4 字节：共享内存槽位索引 */
    int32_t  width{0};           /**< 4 字节：画面像素宽度 */
    int32_t  height{0};          /**< 4 字节：画面像素高度 */
    int32_t  format{0};          /**< 4 字节：像素格式 */
    int32_t  strides[4]{0};      /**< 16 字节：行扫描跨度 */
    uint32_t offsets[4]{0};      /**< 16 字节：槽位物理偏移 */
    int32_t  reserved_fd{-1};    /**< 4 字节：底层硬件 DMA-BUF fd */
    uint32_t reserved_pad{0};    /**< 4 字节：尾部对齐填充字段 */
};                               /**< 结构体总尺寸 96 字节，严格满足 8 字节对齐 */

static_assert(sizeof(FrameDispatchMsg) == 96, "FrameDispatchMsg size mismatch");
static_assert(offsetof(FrameDispatchMsg, capture_time_ms) % 8 == 0, "capture_time_ms unaligned");
```

### 2.2 C ABI 结构体变更 (`native/sdk/include/zhulong/plugin.h`)

#### A. 候选人结构体 `zhulong_face_candidate`
新增 `face_id` 字段，并确保 8 字节对齐：

```c
#define ZHULONG_FACE_ID_MAX_LEN 64

typedef struct zhulong_face_candidate {
    char     person_id[ZHULONG_PERSON_ID_MAX_LEN];     /* 64 字节：底库人员唯一标识符 */
    char     person_name[ZHULONG_PERSON_NAME_MAX_LEN]; /* 64 字节：人员姓名 */
    char     face_id[ZHULONG_FACE_ID_MAX_LEN];         /* 64 字节：★ 方案 B 核心：底库人脸照片唯一标识符 */
    float    similarity;                               /* 4 字节：置信度分值 (0.0 ~ 1.0) */
    uint32_t reserved_;                                /* 4 字节：显式填充对齐 (总 200 字节，8 字节整除) */
} zhulong_face_candidate;
```

#### B. 单人脸项结构体 `zhulong_face_item`
在原有字段基础上，显式增加 `matched_face_id`（Top-1 胜出照片 ID）：

```c
typedef struct zhulong_face_item {
    float    box[4];                                   /* 16 字节：[x1, y1, x2, y2] 归一化坐标 */
    float    quality;                                  /* 4 字节：人脸质量分 */
    float    landmarks[ZHULONG_FACE_LANDMARK_COUNT][2];/* 40 字节：5点关键点坐标 */
    int32_t  matched;                                 /* 4 字节：1=命中底库，0=未命中 */
    float    similarity;                               /* 4 字节：Top-1 匹配置信度 */
    int32_t  is_fused;                                /* 4 字节：是否多帧融合结果 */
    char     person_id[ZHULONG_PERSON_ID_MAX_LEN];     /* 64 字节：Top-1 人员 ID */
    char     person_name[ZHULONG_PERSON_NAME_MAX_LEN]; /* 64 字节：Top-1 人员姓名 */
    char     matched_face_id[ZHULONG_FACE_ID_MAX_LEN]; /* 64 字节：★ Top-1 命中的底照 ID */
    uint32_t candidate_count;                          /* 4 字节：candidates 数组元素数 */
    uint32_t reserved_;                                /* 4 字节：64位指针前置填充对齐 (8 字节对齐) */
    const zhulong_face_candidate* candidates;          /* 8 字节：指向候选人数组指针 */
} zhulong_face_item;
```

### 2.3 Runner 序列化对接 (`runner.cpp`)
在特征比对胜出后，将 `capture_time_ms`、`matched_face_id` 以及 Top-K 候选集的 `face_id` 一并序列化输出至 `MSG_RESULT` JSON 消息：
```json
{
  "stream_id": 1,
  "pts": 123456,
  "capture_time_ms": 1729065600123,
  "cost_ms": 28,
  "matched": true,
  "person_id": "p_01JAX123",
  "person_name": "张三",
  "matched_face_id": "face_front_01",
  "similarity": 0.942,
  "candidates": [
    {
      "person_id": "p_01JAX123",
      "person_name": "张三",
      "face_id": "face_front_01",
      "similarity": 0.942
    }
  ]
}
```

---

## 3. SQLite 数据契约与存储设计

### 3.1 迁移版本号：`000011_create_perception_and_alarm_tables`

严格按照 `.trellis/spec/backend/database-guidelines.md` 规范：
- 主键：统一 ULID（`internal/idutil.New()`），毫秒单调递增，杜绝叶子页分裂；
- **业务主时间戳 (`captured_at`)**：强制采用帧物理抓拍瞬间的 UTC 时间戳，所有检索、排序和历史回放以 `captured_at` 为准；`created_at` 仅用于数据库底层插入时间记账；
- 外键安全：`person_id` 声明为 `REFERENCES persons(id) ON DELETE SET NULL`，绝不因底库人员删除导致历史证据被级联清除；
- 冗余快照：`person_name`、`gallery_image_path` 实行快照存储，底库照片更新或注销不破坏历史证据链；
- 路径安全：所有图片路径均为不带前导斜杠的相对路径（`snapshots/...`）。

```sql
-- 1. 识别记录表
CREATE TABLE IF NOT EXISTS recognitions (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id),
    algorithm_id TEXT NOT NULL REFERENCES algorithms(id),
    pts INTEGER NOT NULL,
    track_id INTEGER NOT NULL DEFAULT 0,
    person_id TEXT REFERENCES persons(id) ON DELETE SET NULL,
    person_name TEXT NOT NULL DEFAULT '',
    matched_face_id TEXT NOT NULL DEFAULT '',
    matched BOOLEAN NOT NULL DEFAULT 0,
    similarity REAL NOT NULL DEFAULT 0.0,
    candidates_json TEXT NOT NULL DEFAULT '[]',
    face_image_path TEXT NOT NULL DEFAULT '',
    gallery_image_path TEXT NOT NULL DEFAULT '',
    image_path TEXT NOT NULL DEFAULT '',
    box_json TEXT NOT NULL DEFAULT '[]',
    body_box_json TEXT NOT NULL DEFAULT '[]',
    attributes_json TEXT NOT NULL DEFAULT '{}',
    captured_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_recognitions_camera_captured ON recognitions(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_recognitions_person ON recognitions(person_id);

-- 2. 告警记录表
CREATE TABLE IF NOT EXISTS alarms (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id),
    algorithm_id TEXT NOT NULL REFERENCES algorithms(id),
    alarm_type TEXT NOT NULL,
    score REAL NOT NULL,
    box_json TEXT NOT NULL DEFAULT '[]',
    image_path TEXT NOT NULL DEFAULT '',
    crop_path TEXT NOT NULL DEFAULT '',
    extra_json TEXT NOT NULL DEFAULT '{}',
    pts INTEGER NOT NULL,
    captured_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_alarms_camera_captured ON alarms(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_alarms_type ON alarms(alarm_type);

-- 3. 抓拍记录表
CREATE TABLE IF NOT EXISTS captures (
    id TEXT PRIMARY KEY NOT NULL,
    camera_id TEXT NOT NULL REFERENCES cameras(id),
    target_type TEXT NOT NULL,
    track_id INTEGER NOT NULL DEFAULT 0,
    score REAL NOT NULL DEFAULT 0.0,
    box_json TEXT NOT NULL DEFAULT '[]',
    crop_path TEXT NOT NULL DEFAULT '',
    image_path TEXT NOT NULL DEFAULT '',
    attributes_json TEXT NOT NULL DEFAULT '{}',
    pts INTEGER NOT NULL,
    captured_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_captures_camera_captured ON captures(camera_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_captures_target_type ON captures(target_type);
```

---

## 4. 业务组件设计 (`internal/record`)

### 4.1 抓拍帧时间锚定与防抖削峰状态机 (Tracking Cooldown)
算法在 5~25 fps 下持续输出检测框，同一目标在视线范围内逗留数十秒。
- **时间锚定**：Record 实体的 `CapturedAt` 严格取自 `time.UnixMilli(inferenceResult.CaptureTimeMs).UTC()`；
- **状态机 Key**：
  - 识别：`rec:{camera_id}:{track_id}`（若 track_id 为 0，则按 `{camera_id}:{person_id}` 防抖）；
  - 告警：`alarm:{camera_id}:{alarm_type}:{track_id}`；
  - 抓拍：`cap:{camera_id}:{target_type}:{track_id}`；
- **冷却窗口**：默认 3 秒（`DefaultCooldown = 3 * time.Second`）。若在冷却窗口内重复收到，只更新内部活跃时间，不写磁盘和数据库。

### 4.2 抓拍落盘与存储防穿透保障
- 文件命名规范：
  - 全景大图：`snapshots/{camera_id}/{YYYY-MM-DD}/{ulid}.jpg`
  - 目标抠图：`snapshots/{camera_id}/{YYYY-MM-DD}/{ulid}_crop.jpg`
  - 人脸抠图：`snapshots/{camera_id}/{YYYY-MM-DD}/{ulid}_face.jpg`
- 存储根路径防穿透：通过 `storage.Service` 获取安全的媒体根，严格杜绝目录穿越。

### 4.3 短事务与无锁并发
- 批量插入（Batch Insert）：当多目标在同一帧命中时，采用短事务一次性提交；
- 严禁在数据库事务中调用 CGO、HTTP 或磁盘 I/O。

---

## 5. API 接口定义

遵循 `internal/httputil` 统一信封格式 `{code: "OK", data: ..., details: ...}`：

| 方法 | 端点 | 核心过滤参数 | 响应数据说明 |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/records/recognitions` | `cameraId`, `personId`, `matched`, `startTime`, `endTime`, `page`, `pageSize` | 分页返回识别记录列表（以 `capturedAt DESC` 排序），包含双图 URL 与 Top-5 候选人 |
| `GET` | `/api/v1/records/alarms` | `cameraId`, `alarmType`, `startTime`, `endTime`, `page`, `pageSize` | 分页返回告警事件列表（以 `capturedAt DESC` 排序），包含现场大图与目标特写 |
| `GET` | `/api/v1/records/captures` | `cameraId`, `targetType`, `startTime`, `endTime`, `page`, `pageSize` | 分页返回抓拍记录列表（以 `capturedAt DESC` 排序），包含结构化属性扩展 |
