# 算法包生命周期管理、看门狗自愈与版本回滚设计 (Design)

## 1. 总体架构与数据流图

```txt
┌────────────────────────────────────────────────────────┐
│                   Zhulong Go 主进程                     │
│                                                        │
│  [POST /api/v1/algorithms/install (Multipart Stream)]  │
│          │                                             │
│          ▼                                             │
│  [Upload Pipeline] ─── (流式落盘 / 魔数校验 / Zip-Slip 防御)
│          │                                             │
│          ▼                                             │
│  [AlgorithmService] ─── (两阶段门禁) ─> zhulong_algo_runner
│          │                                             │
│          ▼                                             │
│  [PackageStore (SQLite)]                               │
│          │                                             │
│          ▼                                             │
│  [ProcessSupervisor]                                   │
│    ├── WorkerWatcher (pkg_1) ──(os/exec)──> Runner 1 (MockDetector)
│    └── WorkerWatcher (pkg_2) ──(os/exec)──> Runner 2 (MockClassifier)
│          │                                             │
│          ▼ (指数退避状态机)                            │
│    [CRASH_LOOP 熔断隔离]                              │
└────────────────────────────────────────────────────────┘
```

## 2. 数据库建模与迁移 (`internal/database/migrations/000007_create_algorithm_tables`)

### `algorithms` 表

```sql
CREATE TABLE IF NOT EXISTS algorithms (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    version TEXT NOT NULL,
    task_type TEXT NOT NULL,                 -- 技术形态：detection, classification, segmentation, pose
    target_arch TEXT NOT NULL,               -- aarch64, x86_64
    target_device TEXT NOT NULL,             -- rk3568, rk3588, cpu
    compute_cost INTEGER NOT NULL DEFAULT 10,-- 单帧算力开销点数 (基于 1,000 点基准池)
    memory_mb INTEGER NOT NULL DEFAULT 64,   -- 预估显存/内存开销 (MB)
    description TEXT NOT NULL DEFAULT '',
    manifest_json TEXT NOT NULL DEFAULT '{}',
    config_schema_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'DISABLED', -- ACTIVE, DISABLED, CRASH_LOOP, ROLLING_BACK
    previous_version TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

> 注：`plugin.so`、`sample.jpg`、`models/`、`.env` 严格遵循“约定大于配置”，路径统一通过 `(algoDir, id, version)` 计算派生，彻底杜绝数据目录迁移或挂载点变动导致的数据库坏死。

### `analysis_tasks` 表

```sql
CREATE TABLE IF NOT EXISTS analysis_tasks (
    id TEXT PRIMARY KEY,
    camera_id TEXT NOT NULL,
    package_name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    sample_fps INTEGER NOT NULL DEFAULT 5,  -- 平台级调度参数：分析抽帧率 (fps)
    config_json TEXT NOT NULL DEFAULT '{}', -- 算法内部超参数 (由 config.schema.json 校验)
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(camera_id, package_name)
);
```

### 独立人员与人脸底库表（`internal/database/migrations/000006_create_persons_tables`）

```sql
-- 人员档案主表
CREATE TABLE IF NOT EXISTS persons (
    id TEXT PRIMARY KEY NOT NULL,                      -- 内部代理主键 (服务端生成，不对外暴露)
    person_id TEXT NOT NULL CHECK (person_id <> ''),   -- 对外业务键：外部系统关联人员的唯一凭据，必填/唯一/不可改
    name TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_person_id ON persons(person_id);

-- 人脸特征明细表 (一人可绑定多张人脸登记照)
CREATE TABLE IF NOT EXISTS person_faces (
    id TEXT PRIMARY KEY NOT NULL,
    person_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,  -- 所属人员的内部行 ID
    image_path TEXT NOT NULL DEFAULT '',
    feature_vector BLOB NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_person_faces_person_id ON person_faces(person_id);
```

> ⚠️ 两张表都有 `person_id` 列，但语义不同：`persons.person_id` 是**对外业务键**，`person_faces.person_id` 是**指向 `persons.id` 的内部行 ID 外键**。为避免 API 客户端把两者当成同一个标识符，`internal/person/model.go` 把 face 的该字段序列化为 `ownerRowId`（JSON 名 `personId` 由 `Person.PersonID` 独占）。
>
> ⚠️ 早期草案中的 `category`（人员分类）与 `card_no`（卡号）**未纳入实现**，已从本文件与 `prd.md` R1.1 同步移除。后续如需追加，走新增迁移，不改写已应用的迁移（见 `spec/backend/database-guidelines.md`）。

## 3. 算法包规范与黑盒验包门禁 (Verification Gate)

### HTTP 上传与多格式解压安全流水线 (Upload Security Pipeline)

1. **流式分块落盘 (Streaming Ingestion)**：
   - 接收 `multipart/form-data` 文件流时，通过 `io.Copy` 流式写入临时文件（`/tmp/zhulong_upload_*.bin`），堆内存开销恒定在缓冲区大小（32KB），彻底防止大模型上传引发 OOM；
   - 限制最大上传大小 `MaxUploadSize = 500MB`，超限立即熔断并中断连接；
2. **多格式魔数自动识别与分流 (Format Magic Routing)**：
   - 读取上传文件头部魔数自动判定归档类型：
     - **`.tar.gz` / `.tgz`**：前 2 字节为 `0x1F, 0x8B`（Gzip 魔数）➔ 进入 TarGz 解压通道；
     - **`.zip`**：前 4 字节为 `0x50, 0x4B, 0x03, 0x04`（`PK\x03\x04`）➔ 进入 Zip 解压通道；
   - 两者均不匹配直接拦截并返回 `UNSUPPORTED_ARCHIVE_FORMAT` 错误；
3. **POSIX 权限自动补全 (Executable Bits Compensation)**：
   - 针对 Windows 环境 zip 打包导致 Linux 可执行权限丢失的问题，解压完成后自动对 `plugin.so` 执行 `os.Chmod(path, 0755)`，防止动态库加载报 `Permission denied`；
4. **Zip-Slip 路径穿越防御 (Path Traversal Protection)**：
   - 无论是 zip 还是 tar.gz，解压每个条目时，必须对相对路径执行 `filepath.Clean`；
   - 强断言目标绝对路径的前缀必须为临时解压目录（`strings.HasPrefix(targetPath, cleanDestDir)`）；
   - 一旦发现 `../` 或试图逃逸出临时目录的条目，立即终止解压、抛出安全错误并记录审计日志；
5. **解压膨胀炸弹防御 (Decompression Bomb Protection)**：
   - 使用带计数的读取器累计解压体积，解压总大小上限为 1GB，文件数上限为 1,000 个，超限立即阻断；
6. **原子暂存与生命周期清理**：
   - 所有操作在隔离的临时目录进行；
   - 仅当门禁全量通过后，才通过 `os.Rename` 原子移动至 `/var/lib/zhulong/algorithms/<id>/<version>/`；
   - 若任何一步失败，`defer os.RemoveAll(...)` 保证 100% 清理所有临时文件，防止磁盘碎片堆积。

### 约定优于配置的文件拓扑 (Directory Layout Conventions)

```text
my_algorithm.tar.gz
├── manifest.json              # 核心元数据（身份、双层类型、平台、算力点数）
├── config.schema.json         # 算法超参数 JSON Schema（可选，无参数则可缺省）
├── .env                       # [可选] 本地环境变量配置文件（免重编调参/驱动控制）
├── plugin.so                  # [约定] 入口动态库，无需在配置中声明路径
├── sample.jpg                 # [约定] 验包样本图，无需在配置中声明路径
└── models/                    # [约定] 模型权重目录，无需在配置中声明路径
    └── weights.rknn
```

### 免重编环境覆写与四层配置金字塔 (`.env`)

为了让算法工程师在现场调参时**彻底摆脱重新交叉编译 `.so` 和重新打包的繁琐循环**，看门狗在启动各算法 Runner 子进程时，支持自动挂载算法包目录下的 `.env` 文件：

```bash
# /var/lib/zhulong/algorithms/<id>/<version>/.env
RKNN_LOG_LEVEL=3               # 底层 RKNPU2 驱动日志等级
RGA_DEBUG=1                    # 底层 RGA 硬件加速调试
OMP_NUM_THREADS=2              # OpenMP 线程池大小
CONFIDENCE_THRESHOLD=0.35      # 覆盖默认置信度
```

四层参数配置覆盖优先级：

1. **最高优先级**：摄像头级动态参数（Web 界面保存至 `algorithm_bindings.config_json`）；
2. **运维优先级**：包级本地 `.env` 文件（现场热调优，改动后重启 Runner 秒级生效）；
3. **推荐默认值**：包级元数据默认值（`config.schema.json` 中的 `default` 声明）；
4. **底层兜底值**：插件 C++ 源码内硬编码默认值（防空指针保护）。

### `manifest.json` 规范示例

```json
{
  "id": "safety_helmet_detector",
  "version": "1.0.0",
  "name": "安全帽佩戴检测算法",
  "description": "基于 YOLOv8 的实时工地安全帽与反光衣检测",

  "task_type": "detection",       // 技术形态：detection / classification
  "category": "safety",           // 业务分类：safety / security / fire / traffic / general

  "platform": {
    "arch": "aarch64",
    "target": "rk3568"
  },

  "resources": {
    "compute_cost": 30,           // 单帧算力开销点数 (基于系统 1,000 分基准总池)
    "memory_mb": 128              // 模型权重与工作区预估内存 (MB)
  }
}
```

### 算法推理结果统一输出规范 (Inference Result Output Specification)

为彻底解耦算法模型、上层业务与前端渲染，所有算法插件的推理结果必须严格遵循以下规范：

#### 1. 目标检测输出 (`task_type: "detection"`)

```json
{
  "camera_id": "cam_gate_01",
  "package_id": "safety_helmet_detector",
  "pts": 1728800000123,
  "timestamp": "2026-10-12T10:00:00.123Z",
  "task_type": "detection",
  "cost_ms": 28,
  "detections": [
    {
      "box": [0.254, 0.128, 0.412, 0.385],
      "label": "helmet",
      "score": 0.94
    },
    {
      "box": [0.612, 0.340, 0.789, 0.850],
      "label": "person",
      "score": 0.88
    }
  ]
}
```

#### 2. 图像/状态分类输出 (`task_type: "classification"`)

```json
{
  "camera_id": "cam_conveyor_02",
  "package_id": "industrial_defect_classifier",
  "pts": 1728800000456,
  "timestamp": "2026-10-12T10:00:00.456Z",
  "task_type": "classification",
  "cost_ms": 15,
  "classifications": [
    {
      "label": "normal",
      "score": 0.98
    }
  ]
}
```

#### 3. 核心设计铁律

- **无冗余设计**：彻底剔除模型底层索引 `class_id`，仅保留语义化 `label`；
- **强制归一化**：$0.0 \le x_1 < x_2 \le 1.0$，$0.0 \le y_1 < y_2 \le 1.0$，彻底屏蔽摄像头物理分辨率与播放器窗口缩放；
- **PTS 时钟保真**：输出事件携带原帧 `pts`，前端 Canvas/SVG 与视频解码实现毫秒级音画框帧同步；
- **验包门禁强检**：验包门禁在自测样本时，自动扫描所有坐标与置信度，若发现数值越界（如返回绝对像素 640），门禁立即判定验包失败并拒绝入库。

#### 3. 人脸-人体结构化输出与 1:N 多脸挂载模型 (`task_type: "person_structure"`)

针对人脸识别、视频结构化等复合视觉任务，算法在内部完成多帧时序特征融合与轨迹平滑后，直出人员身份；**一个人体支持挂载 0 到多个识别人脸**（如常规单人、背影无脸、抱小孩/背人双脸等复杂工业级工况）：

```json
{
  "camera_id": "cam_gate_01",
  "package_id": "yolo_face_reid_rknn",
  "pts": 1728800000123,
  "timestamp": "2026-10-12T10:00:00.123Z",
  "task_type": "person_structure",
  "cost_ms": 42,
  "persons": [
    {
      "track_id": 101,                         // 目标跟踪 ID
      "body_box": [0.250, 0.120, 0.450, 0.850], // 人体全身框 [x1, y1, x2, y2]
      "body_score": 0.95,
      // 一个人体挂载的人脸列表 (支持 0 ~ N 个)
      "faces": [
        {
          "box": [0.310, 0.130, 0.390, 0.280],   // 人脸特写框 (自然落在 body_box 内部)
          "quality": 0.92,                        // 综合人脸质量分
          "landmarks": [                          // 5 点关键点
            [0.335, 0.180], [0.365, 0.178], [0.350, 0.215],
            [0.338, 0.245], [0.362, 0.243]
          ],
          // ★ Top-1 最佳匹配主项 (方便绝大多数常规业务直接消费) ★
          "matched": true,                        // 算法内部比对命中
          "person_id": "emp_9527",                // 底库人员工号/ID
          "person_name": "张三",                  // 姓名
          "similarity": 0.94,                     // 时序特征融合后的比对置信度
          "is_fused": true,                       // 标记已进行多帧加权融合

          // ★ Top-K 候选人列表 (按人员 person_id 去重聚合后按相似度降序) ★
          "candidates": [
            {
              "person_id": "emp_9527",
              "person_name": "张三",
              "similarity": 0.94
            },
            {
              "person_id": "emp_1002",
              "person_name": "李四",
              "similarity": 0.82
            },
            {
              "person_id": "emp_3045",
              "person_name": "王五",
              "similarity": 0.76
            }
          ]
        }
      ]
    },
    {
      "track_id": 102,
      "body_box": [0.550, 0.200, 0.800, 0.900],
      "body_score": 0.93,
      // 抱小孩或紧密重叠场景：单人体挂载 2 张人脸
      "faces": [
        {
          "box": [0.600, 0.210, 0.680, 0.320],
          "quality": 0.90,
          "landmarks": [ ... ],
          "matched": true,
          "person_id": "emp_8888",
          "person_name": "李四(大人)",
          "similarity": 0.91,
          "is_fused": true
        },
        {
          "box": [0.680, 0.260, 0.750, 0.360],
          "quality": 0.82,
          "landmarks": [ ... ],
          "matched": false,
          "person_id": "",
          "person_name": "同行儿童",
          "similarity": 0.35,
          "is_fused": false
        }
      ]
    },
    {
      "track_id": 103,
      "body_box": [0.800, 0.400, 0.950, 0.950],
      "body_score": 0.88,
      "faces": []                              // 背影无脸时为空数组
    }
  ]
}
```

#### 4. 方式 B：IPC 动态底库热更新与「一人多照」注册设计 (Multi-Face Gallery Synchronization)

```txt
┌────────────────────────────────────────────────────────┐
│            Go 业务层 (SQLite 集中底库存储)               │
│  [gallery_persons: 张三] ──(1:N)──> [gallery_faces]    │
│                                       ├── 照片1: 正脸照  │
│                                       ├── 照片2: 戴眼镜  │
│                                       └── 照片3: 微侧脸  │
└───────────────────────────┬────────────────────────────┘
                            │ UDS IPC (二进制控制通道)
                            ▼
┌────────────────────────────────────────────────────────┐
│  zhulong_algo_runner 子进程 (内存多特征向量索引)         │
│    • MSG_GALLERY_SYNC   (冷启动全量下发所有模板)        │
│    • MSG_GALLERY_UPSERT (增量注入新特征向量+所属人员)   │
│    • MSG_GALLERY_DELETE (根据 face_id / person_id 移除) │
│                                                        │
│  【视频人脸命中任意一张模板 ➔ 均准确归属到该人员工号】 │
└────────────────────────────────────────────────────────┘
```

1. **一人多照模型 (Multi-Template Matching)**：
   - 现实中单张登记照极易因发型、眼镜、季节光照产生漏识；系统支持为同一个人员录入多张照片（例如常规正脸、戴眼镜通勤照、侧脸照）；
   - 每张照片提取独立的 512 维特征向量，但均记录 `person_id: "emp_9527", person_name: "张三"`；
   - 算法子进程在内存维护向量矩阵检索时，视频抓拍人脸与张三的**任意一个特征向量比对相似度超过阈值**，即命中张三，并返回最高相似度！
2. **底库生命周期集中由 Go 掌控**：
   - SQLite 维护 `gallery_persons`（主表）与 `gallery_faces`（明细表，带级联删除）；
   - Web 界面增删人员或追加/删除登记照只与 Go 交互，底库持久化有单一真实源；
3. **IPC 动态更新、零重启**：
   - 算法 Runner 启动握手完成后，Go 主动推送 `MSG_GALLERY_SYNC` 注入初始底库；
   - 运维在 Web 端随时增删人员或单张照片，Go 实时向在途 Runner 广播 `MSG_GALLERY_UPSERT` 或 `MSG_GALLERY_DELETE`；
   - 算法子进程动态更新自身内存中的向量索引，拉流推理**全程不中断、不重启**。

#### 5. 业务告警事件模型决策（单目标独立事件 + 共享全景大图）

当一帧画面检测出多个违规目标时，业务层与告警存储采用**方案一（目标级独立事件）**：

- **全景底图单次落盘**：该帧全景原画仅编码落盘一次（如 `/snaps/<batch_id>_full.jpg`），避免大图膨胀；
- **单目标独立事件与特写抠图**：
  - 针对每个检测到的目标，生成一条独立的事件实体（`event_id`），包含自身的 `crop_image_url`、归一化 `box`、`label` 与 `score`；
  - 拥有独立的业务工单状态机（`status: UNPROCESSED / CONFIRMED / IGNORED`），消警与误报剔除彻底解耦；
  - 携带统一的 `batch_id`（基于 `camera_id` + `pts`）与共享的 `full_image_url`，保证前端卡片既能展示独立特写头像，点击后又能关联同一全景现场。

### 算力点数配额模型 (Compute Points Budget Model)

- **基准总池**：全设备标准化设定为 **1,000 点**算力池；
- **单路任务消耗**：`任务配额 = compute_cost * sample_fps`（例如 $30 \times 5\text{fps} = 150$ 点）；
- **容量水位审计**：摄像头绑定算法时，系统累计各启用绑定的总点数；当总点数超过 1,000 点时，前端亮起容量超载黄色警告或阻止保存，杜绝 NPU 队列拥塞与崩溃。

### `config.schema.json` 规范示例（纯算法超参数，无平台调度参数）

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "confidence_threshold": {
      "type": "number",
      "title": "置信度阈值",
      "description": "检测框输出过滤阈值",
      "default": 0.45,
      "minimum": 0.1,
      "maximum": 1.0
    },
    "nms_threshold": {
      "type": "number",
      "title": "NMS 重叠过滤阈值",
      "default": 0.45,
      "minimum": 0.1,
      "maximum": 0.95
    },
    "filter_classes": {
      "type": "array",
      "title": "关注目标类别",
      "items": { "type": "string" },
      "default": ["helmet", "person"]
    }
  },
  "required": ["confidence_threshold"]
}
```

### 两阶段验包执行时序 (Two-Stage Verification)

1. **解压与约定检查**：
   - 用户上传 `.tar.gz` 算法包，解压至临时隔离目录 `/tmp/algo_verify_<random>/`；
   - 检查必须存在 `manifest.json`，且根目录存在约定的 `plugin.so` 与 `sample.jpg`；
   - 解析 `manifest.json` 与可选的 `config.schema.json`；
2. **第一阶段：静态契约与平台兼容性拦截**：
   - 读取当前宿主硬件架构与设备型号（例如 `aarch64 / rk3568`）；
   - 比对 `manifest.json` 中的 `platform.arch` 与 `platform.target`，不匹配返回 `ALGO_PLATFORM_MISMATCH`；
   - 校验 `task_type` 与 `category` 枚举，提取 `compute_cost` 与 `memory_mb`；
3. **第二阶段：动态黑盒自测**：
   - 执行原生验包命令：

     ```bash
     zhulong_algo_runner --verify-plugin plugin.so --sample sample.jpg
     ```

   - 读取标准输出，解析最后一行 JSON 报告（`status == "passed"`）；
   - 校验 `plugin.so` 报告的 `result_type` 与 `manifest.json` 中的 `task_type`（1=detection, 2=classification）绝对一致；
4. **持久化与激活**：
   - 验包成功：将临时目录重命名移至持久化存储目录 `/var/lib/zhulong/algorithms/<id>/<version>/`，写入 SQLite；
   - 验包失败：清理临时目录，返回具体结构化报错。

## 4. 进程看门狗与有界自愈状态机 (Watchdog & State Machine)

### 状态机定义

- `DISABLED`：未启动；
- `STARTING`：正在启动并等待就绪；
- `RUNNING`：健康运行中；
- `CRASHED`：进程异常退出，正在等待退避倒计时；
- `CRASH_LOOP`：熔断隔离，停止自动拉起；
- `ROLLING_BACK`：正在回滚到上一个稳定版本。

### 有界自愈策略 (Bounded Recovery)

- 每次崩溃后，增加连续崩溃计数 `crash_count++`；
- 计算本次退避时长：`backoff = min(1s * 2^(crash_count - 1), 30s)`；
- 记录崩溃时间戳至滑动窗口（窗口跨度 5 分钟）；
- 若滑动窗口内崩溃次数**超过允许配额**（`max_crashes = 3` 表示窗口内最多允许崩溃 3 次，第 4 次才熔断）：
  - 转移至 `CRASH_LOOP` 状态；
  - 触发告警通知并记录审计日志；
  - 停止重试计时器；
  - 注：3 次配额下实际可观察到的退避档为 1s/2s/4s，`8s` 档（`shift=3`）在本配额下不可达——退避阶梯描述公式形态，不保证每一档都被执行。
- 若进程成功稳定存活超过 10 分钟：
  - 自动重置 `crash_count = 0`，清空滑动窗口。

## 5. 安全重启升级与受控回滚机制 (Upgrade & Rollback)

### 升级时序

1. 安装并验包新版本 `V2`（验包门禁保证 ABI 与基础推理正常）；
2. 记录当前版本 `V1` 作为回退副本（`previous_version`, `previous_entry_path`）；
3. 向 `V1` 进程发送 `SIGTERM`，等待安全退出；
4. 拉起新版本 `V2` 进程，进入 `STARTING` 状态；
5. 开启 `RollbackGuardTimer`（5 秒就绪观察期）。

### 回滚时序 (Rollback Trigger)

1. 若 `V2` 进程在 5 秒观察期内异常崩溃或无法建立通信：
   - 触发自动回滚，状态切换为 `ROLLING_BACK`；
   - 记录警告日志：“新版本 V2 启动失败，触发自动回滚至 V1”；
   - 使用 `previous_entry_path` 重新拉起 `V1` 进程；
   - 若 `V1` 恢复成功，状态重置为 `RUNNING`，并标记 `status = 'ACTIVE'`（保持原旧版本有效，将 V2 标记为故障）；
   - 若 `V1` 亦启动失败，直接切换至 `CRASH_LOOP` 隔离并告警，**坚决不继续震荡**！

## 6. Uber Fx 架构与模块边界

- 模块名：`algorithmModule = fx.Module("algorithm", ...)`
- 遵循统一规范：
  - 构造函数仅初始化内存结构与配置；
  - `fx.Hook` 的 `OnStart` 中注册后台监控协程；
  - `OnStop` 负责向所有在途 Runner 进程发送优雅退出信号，并等待回收。
