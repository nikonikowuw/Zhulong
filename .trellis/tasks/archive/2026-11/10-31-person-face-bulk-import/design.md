# 人员人脸工业级压缩包批量导入 技术设计 (Technical Design)

## 1. 架构总览与模块边界

系统引入了通用的系统级后台异步任务子系统（`internal/asyncjob`），将长耗时、重型计算任务从业务领域中彻底解耦。人员模块作为该异步底座的注册执行器（`JobHandler`）：

```text
[Web SPA (React)]
    │  POST /api/v1/persons/import-face-jobs (Multipart ZIP)
    ▼
[Gin HTTP Handler (internal/person)]
    │  1. 鉴权 & 限流 & 基础 Header 校验
    │  2. 安全落盘至 data/tmp/face_import_<jobId>/upload.zip
    │  3. 委托 asyncjob.Service.SubmitJob("person.face_import", metadata)
    │  4. 立即响应 202 Accepted { jobId }
    ▼
[系统通用异步任务引擎 (internal/asyncjob)]
    │  表: system_async_jobs (统一持久化状态与进度)
    │  状态流转: PENDING -> PROCESSING -> COMPLETED/FAILED/CANCELLED
    │  (全局 QoS 算力保护，单 Worker 受控调度，意外宕机孤儿自动回收)
    ▼
[FaceImportJobHandler (internal/person)]
    ├── 1. Zip 安全解压与防御 (Zip Slip 路径校验 + Zip Bomb 动态 LimitReader 截断)
    ├── 2. Manifest 清单解析 (解析 manifest.csv 或 fallback 目录/文件名自适应正则扫描)
    ├── 3. 逐图 AI 视觉质检门禁 (微秒级 Yield 让渡，保障实时监控流 RTSP AI 优先):
    │      - 像素防爆与图片解码 (宽高 ≤ 4096)
    │      - 人脸检出计数 (SCRFD 置信度 ≥ 0.5):
    │            count == 0 -> NO_FACE_DETECTED (拒绝)
    │            count >= 2 -> MULTIPLE_FACES_DETECTED (工业硬律硬性拒绝，杜绝合照盲猜)
    │      - 姿态偏角评估 (Yaw/Pitch > 35° -> FACE_POSE_EXCEED)
    │      - 人脸尺寸评估 (BBox 短边 < 80px -> FACE_TOO_SMALL)
    │      - ArcFace 提取 512 维 float32 特征向量
    ├── 4. 事务持久化 (Store):
    │      - 新人员自动创建 (persons 表)
    │      - 存量人员一人多照追加 (faces 表，校验姓名一致性)
    │      - 实时回调 updater.Update 推进任务进度
    └── 5. 底库批量热同步 (Gallery Sync Aggregation):
           - 聚合所有新增人脸特征，触发一次 MSG_GALLERY_SYNC 广播至各个 Runner
    │
[RAII 存储清理]
    └── 任务终止 (无论是成功、失败或取消)，彻底删除 data/tmp/face_import_<jobId>
```

---

## 2. API 接口契约

### 2.1 创建人脸批量导入任务
- **Endpoint**: `POST /api/v1/persons/import-face-jobs`
- **Content-Type**: `multipart/form-data`
- **Form Data**:
  - `file`: ZIP 压缩包二进制流（上限 200 MiB）
- **Response**: `202 Accepted`
```json
{
  "code": "OK",
  "message": "导入任务已创建",
  "data": {
    "jobId": "job_01j7q3x...",
    "status": "PENDING"
  }
}
```

### 2.2 轮询/查询任务进度
- **Endpoint**: `GET /api/v1/persons/import-face-jobs/:jobId`
- **Response**: `200 OK`
```json
{
  "code": "OK",
  "message": "成功",
  "data": {
    "jobId": "job_01j7q3x...",
    "status": "PROCESSING",
    "total": 350,
    "processed": 128,
    "succeeded": 125,
    "failed": 3,
    "currentFile": "images/EMP002_2.jpg",
    "createdAt": "2026-10-31T20:00:00Z",
    "updatedAt": "2026-10-31T20:00:45Z"
  }
}
```

### 2.3 下载失败诊断报告
- **Endpoint**: `GET /api/v1/persons/import-face-jobs/:jobId/failed-report`
- **Response**: `200 OK` (`text/csv; charset=utf-8`)
- **CSV Format**:
```csv
line,filePath,name,personId,errorCode,message
12,images/pic12.jpg,张三,EMP-001,NO_FACE_DETECTED,未检测到人脸
45,images/team.jpg,李四,EMP-002,MULTIPLE_FACES_DETECTED,检测到多张人脸（合影照拒绝录入）
89,images/bad.png,王五,EMP-003,CORRUPT_OR_OVERSIZED_IMAGE,图片损坏或分辨率超标
```

---

## 3. 纵深安全防御机制 (Security Hardening)

### 3.1 Zip Slip 路径逃逸防御
在解压每个 `*zip.File` 时，必须使用严格的安全清洗函数：
```go
cleanPath := filepath.Clean(filepath.Join(destDir, file.Name))
if !strings.HasPrefix(cleanPath, filepath.Clean(destDir) + string(os.PathSeparator)) {
    return apperr.New(apperr.KindSecurity, "ZIP_SLIP_ATTEMPT", "非法文件路径")
}
```

### 3.2 Zip Bomb 解压炸弹防御
- `totalExtractedBytes uint64` 累加器；
- 每次解压调用 `io.LimitReader`；
- 若单个文件解压或总解压量超过 `1 GiB`，或者总文件数超过 `1000`，立即中断任务并返回 `ZIP_BOMB_DETECTED` 错误，同时调用 `os.RemoveAll` 清除全部临时文件。

### 3.3 图片像素防爆 (Decompression Bomb)
在完整载入并解码图片前，先调用 `image.DecodeConfig` 获取元信息：
```go
config, _, err := image.DecodeConfig(fileReader)
if config.Width > 4096 || config.Height > 4096 {
    return ErrOversizedImage
}
```

---

## 4. QoS 算力让渡与底库批量同步

1. **算力隔离 (Fair-share QoS)**：
   - 使用单容量 Channel 的 Worker 协程，严格保证批量导入是顺序/低并发运行，杜绝并发调用底座人脸推理造成算力击穿。
   - 在每张照片推理完成后，显式注入 `time.Sleep(5 * time.Millisecond)` 或主动协程让渡，保证高优先级的实时 RTSP 帧解码与抓拍分析调度顺畅。
2. **底库同步风暴抑制**：
   - 维护一个增量人脸 ID 列表 `newFaceIDs []string`；
   - 批量导入完成后，执行一次 `s.broadcastUpsert(ctx, newFaces)` 统一广播底库热更新，避免单张频繁 IPC 广播造成消息队列溢出。

---

## 5. 持久化与存储结构

### 5.1 数据库迁移
在 `internal/database/migrations/` 追加系统统一异步任务迁移脚本：
- `system_async_jobs`:
  - `id` (TEXT, PK)
  - `type` (TEXT, NOT NULL, INDEX) - 任务类型（例如 `"person.face_import"`）
  - `status` (TEXT, NOT NULL, INDEX) - 状态（PENDING, PROCESSING, COMPLETED, FAILED, CANCELLED）
  - `progress` (INTEGER, 0-100)
  - `total` / `processed` / `succeeded` / `failed` (INTEGER)
  - `current_stage` / `current_file` (TEXT)
  - `metadata_json` / `result_json` (TEXT)
  - `error_message` (TEXT)
  - `created_at` / `updated_at` / `finished_at` (DATETIME)

### 5.2 临时目录 RAII
临时目录格式：`data/tmp/face_import_<jobId>`
在任务结束的 `defer` 中保证：
```go
defer func() {
    _ = os.RemoveAll(jobDir)
}()
```
并在应用启动钩子（Fx OnStart）中扫描并清理遗留的历史孤儿目录。
