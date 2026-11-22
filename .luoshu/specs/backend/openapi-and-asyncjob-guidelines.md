# M2M OpenAPI 与后台异步任务规范

> `internal/openapi`（机器间接口与上传并发控制）及 `internal/asyncjob`（持久化异步作业状态机）的核心契约。

---

## 1. M2M OpenAPI (`internal/openapi` + `internal/apitoken`)

### 1.1 路由组与鉴权边界
- **独立挂载组**：全部端点通过 `group:"openapi_routes"` 挂载于 `/api/v1/openapi`，由 `apitoken.RequireBearer` 执行 M2M Bearer Token 鉴权并升级审计 Actor（默认回落 `"API"`），**严禁复用控制台 Cookie Session**。
- **嵌套请求体上限**：`/api/v1/openapi` 根组为人员人脸照片上传开放 **11 MiB** 上限；普通 JSON 端点（如 `POST /openapi/persons/:personId`）必须挂入嵌套的 `MaxBodyLimit()`（1 MiB）子组，防止大 JSON 耗尽边缘设备内存。

### 1.2 上传并发限流（防资源泄漏与 i18n 冲突）
- **`UploadLimiter` 双层信号量**：全局最大并发 `GlobalMaxFaceUploads = 3`，单 Token 最大并发 `TokenMaxFaceUploads = 1`。
- **引用计数回收 Token 槽位**：`tokenSlot` 跟踪 `refs`，最后一个持有者释放时从 `tokenSems` 删除对应条目，防止运维反复新建/吊销 Token 造成通道内存泄漏。
- **错误码独占**：并发限流返回独立机器码 `FACE_UPLOAD_CONCURRENCY_LIMITED`（不复用通用的 `TOO_MANY_REQUESTS`），避免与 `clientlog` / IP 限流器在全局 i18n 字典中互相覆盖。

### 1.3 Swagger 与文档路径对账门禁
- Swagger `@Router` 注解使用相对根路径（如 `/openapi/persons/{personId}`），不重复 `/api/v1`。
- `internal/openapi/contract_test.go` 直接解析 `swag.ReadDoc()` 的生成物与实际 Gin 路由表、第三方对接文档交叉对账，禁止另建手抄路径清单。

---

## 2. 持久化后台异步任务 (`internal/asyncjob`)

### 2.1 单向不可逆状态机
- 合法状态流转严格单向向前：`PENDING ➔ EXTRACTING | PROCESSING ➔ COMPLETED | FAILED | CANCELLED`。
- **条件更新防复活**：认领、进度更新与终态落盘的 SQL 条件均限定在 `activeStatuses`（`PENDING` / `EXTRACTING` / `PROCESSING`）内，任何迟到的 worker 写入都无法覆盖已结束或已取消的终态行。
- 阶段（`JobStage`）与状态（`JobStatus`）分离，用于向前端展示更细的执行阶段。

### 2.2 有界队列、超时与重启孤儿恢复
- **有界排队**：内存队列深度上限 `defaultQueueDepth = 128`，满队直接拒绝并返回 `ASYNC_JOB_QUEUE_FULL`，不允许无界堆积。
- **单次 DB 写入限时**：worker 每次持久化操作带 `databaseTimeout = 10s` 上限，防止慢磁盘或锁竞争无限拖死取消与停机。
- **机器码持久化（非原始文本）**：终态失败将稳定机器码写入 `system_async_jobs.error_code`（如 `JOB_INTERRUPTED`、`JOB_SHUTDOWN`、`JOB_PANICKED`），不保存底层原始错误文本；重启后 `RecoverOrphanJobs` 自动将未完成的孤儿任务标记为失败终态。

### 2.3 生命周期顺序
- `asyncJobs.Start` 必须在 `person.Start` 清理陈旧临时目录（stale staging）**之后**运行，避免刚拉起的 worker 与清理动作竞态；
- `asyncJobs.Stop` 必须在 `person.Stop`、`algorithm.Stop` 与 `native.Close` **之前**停止并等待在途任务排空。
