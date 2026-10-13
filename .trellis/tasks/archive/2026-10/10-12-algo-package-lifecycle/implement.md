# 算法包生命周期管理、看门狗自愈与版本回滚实施计划 (Implement)

## 实施顺序

### 阶段一：数据库持久层与领域模型定义
- [x] 编写 SQLite 迁移文件 `internal/database/migrations/000007_create_algorithm_tables.up.sql` 与 `down.sql`（包含 `algorithm_packages`、`algorithm_bindings`，彻底去除冗余路径列）；
- [x] 独立人员底库领域迁移 `internal/database/migrations/000006_create_persons_tables.up.sql` 与 `down.sql`（包含 `persons`、`person_faces` 1:N 人脸模板明细，`person_id` 对外业务键 NOT NULL CHECK + UNIQUE 索引）；
- [x] 编写算法领域实体 `internal/algorithm/model.go`（纯约定路径计算 `EntryPath(algoDir)`, `SamplePath(algoDir)`, `EnvPath(algoDir)`, `ModelsDir(algoDir)`）；
- [x] 编写独立人员领域模块 `internal/person/`（`model.go`, `store.go`, `service.go`, `handler.go`, `locale.go`, `module.go`，含一人多照 CRUD 逻辑；`person_id` 必填/唯一/不可变契约，单元测试 `store_test.go`, `service_test.go`, `handler_test.go`）；
- [x] 编写标准推理输出实体 `internal/algorithm/result.go`（`InferenceResult`, `DetectionItem`, `ClassificationItem`, `PersonStructureResult`, `PersonItem`, `FaceSubItem` 包含 Top-K `Candidates []FaceCandidate`），定义统一的无 `class_id`、纯语义 `label`、归一化 `box`、多脸挂载人体结构化模型与直出身份/Top-K 候选人实体；
- [x] 编写数据库存储实现 `internal/algorithm/store.go` 与单元测试 `store_test.go`。

### 阶段二：算法包上传安全、多格式解压与黑盒验包门禁
- [x] 编写 `internal/algorithm/uploader.go`：
  - 实现流式 Multipart 文件接收，限制最大 500MB，流式分块写入磁盘防 OOM；
  - 自动识别文件魔数：支持 `.tar.gz`（`0x1F, 0x8B`）与 `.zip`（`0x50, 0x4B, 0x03, 0x04`），实现双格式安全分流解压；
  - 自动补全 Windows 打包可能丢失的 `plugin.so` `0755` 执行权限；
  - 实现安全解压器：带 `filepath.Clean` 的 Zip-Slip 路径防穿越断言；
  - 实现解压膨胀炸弹计数防御（最大累计 1GB 解压量、最多 1,000 个文件）；
  - 实现临时暂存目录生命周期守护，`defer` 确保异常时 100% 清理；
- [x] 编写 `internal/algorithm/verifier.go`：
  - 校验约定的 `plugin.so`、`sample.jpg` 与 `manifest.json` 存在性；
  - 解析 `manifest.json` 与可选的 `config.schema.json`、`.env`；
  - **第一阶段门禁**：
    - 校验 `platform.arch` 与 `platform.target` 是否与当前宿主兼容，不兼容返回 `ALGO_PLATFORM_MISMATCH`；
    - 校验 `task_type`（detection/classification）与 `category`（safety/security/fire/traffic/general）合法性；
    - 提取 `resources: { compute_cost, memory_mb }` 并做正数边界校验；
  - **第二阶段门禁**：调度 `zhulong_algo_runner --verify-plugin plugin.so --sample sample.jpg`；
  - 校验返回码与严格解析最后一行的 JSON 结果，核对底层 ABI `result_type` 与 `task_type` 一致性；
  - 验包成功后原子重命名至正式持久化目录，验包失败清理临时文件并返回明确错误；
- [x] 编写单元测试 `uploader_test.go` 与 `verifier_test.go`（覆盖 `.tar.gz` 与 `.zip` 正常上传解压、魔数非法拦截、Zip-Slip 攻击拦截、膨胀炸弹拦截、Windows 权限补全、平台不匹配、类型不匹配、算力参数越界、缺少约定文件、非法 schema、动态验包失败与成功场景）。

### 阶段三：Runner 进程监督、.env 注入与底库热更新 IPC
- [x] 编写 `internal/algorithm/process.go` 与 `supervisor.go`：
  - 封装单进程 Watcher（`os/exec.Cmd`、捕获日志管道、状态流转通知）；
  - 实现 `.env` 文件解析器（`loadDotEnv`），启动 Runner 时将环境变量追加注入至 `cmd.Env`，实现免重编调参与驱动控制；
  - 实现 UDS IPC 动态底库热更新调度器（`GalleryDispatcher`）：支持冷启动 `MSG_GALLERY_SYNC` 全量下发与运行期 `MSG_GALLERY_UPSERT`/`MSG_GALLERY_DELETE` 增量热更新；
  - 实现优雅退出时序：`SIGTERM` ➔ 等待最多 3 秒 ➔ 超时 `SIGKILL` ➔ 汇合 PID；
  - 编写进程生命周期与 `.env` 环境变量注入单元测试 `process_test.go`。

### 阶段四：有界退避自愈状态机与回滚控制
- [x] 编写 `internal/algorithm/recovery.go`：
  - 实现带滑动窗口与指数退避的状态机（1s, 2s, 4s, 8s, 16s, 30s）；
  - 连续崩溃 3 次熔断至 `CRASH_LOOP`，防止重启雪崩；
  - 稳定运行 10 分钟自动重置计数器；
- [x] 编写升级观察守卫与版本回滚逻辑（`RollbackGuard`）：
  - 升级后启动 5 秒就绪观察期，窗口内崩溃自动退回旧版并告警；
- [x] 编写自愈与回滚综合单元测试 `recovery_test.go`。

### 阶段五：业务服务装配、RESTful API 与 Fx 接入
- [x] 编写 `internal/algorithm/service.go`：整合 Store、Verifier、Supervisor，暴露标准 CRUD、激活、停用、绑定摄像头等业务方法；
- [x] 编写 `internal/algorithm/handler.go`：提供标准 HTTP RESTful 控制面接口；
- [x] 在 `internal/app/app.go` 注册 `algorithmModule`，接入 Fx 生命周期（`OnStart` 启动已激活算法，`OnStop` 优雅停用所有 Runner 进程）；
- [x] 编写 HTTP 接口测试 `handler_test.go` 与整体验收测试。

### 阶段六：质量验证与门禁检查
- [x] 运行 `go vet ./...`；
- [x] 运行 `go test -race ./internal/algorithm/...`；
- [x] 运行 `make go-check` 与 `make smoke` 验证全栈集成通过。
