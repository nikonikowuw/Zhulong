# 算法包生命周期管理、看门狗自愈与版本回滚 (PRD)

## 1. 目标与背景

本任务为算法插件架构父任务（`.trellis/tasks/10-10-algorithm-plugin-architecture/`）的**阶段三**交付物。
在阶段一（SDK、运行库、Runner 与验包 CLI）与阶段二（跨进程 FrameBroker 与共享内存租赁）中，我们已成功打通原生底座与跨进程数据流。
阶段三的核心目标是**构建 Go 业务层的算法包生命周期调度中枢与进程看门狗**：
- 实现算法包安装、解压与黑盒验包门禁调度；
- 实现一包一进程的 Runner 进程守护与优雅退出；
- 实现有界指数退避与防重启风暴自愈状态机；
- 实现包级安全升级与故障自动回滚机制。

## 2. 核心需求 (Requirements)

### R1. 算法包与绑定配置数据模型 (`algorithm_packages`, `algorithm_bindings`)
- 新增 SQLite 迁移版本（`000007_create_algorithm_tables`）；
- `algorithm_packages`：记录包 ID、名称、版本、任务形态 `task_type`（`detection`/`classification`）、业务分类 `category`（`safety`/`security`/`fire`/`traffic`/`general`）、目标架构 `target_arch`、目标设备 `target_device`、单帧算力开销点数 `compute_cost`、预估内存 `memory_mb`、原始 `manifest_json`、参数模式 `config_schema_json`、状态（`ACTIVE`, `DISABLED`, `CRASH_LOOP`, `ROLLING_BACK`）、是否激活、创建与更新时间；遵循约定优于配置，动态库 `plugin.so`、样本图 `sample.jpg`、模型 `models/` 与环境变量 `.env` 路径由基础目录派生，不硬编码至数据库；
- `algorithm_bindings`：记录单路摄像头与算法包的任务关联、自定义配置 JSON（经 `config.schema.json` 校验）、平台调度抽帧率 `sample_fps`、以及启停状态。

### R1.1 独立人员与人脸底库域 (`internal/person` & `000006_create_persons_tables`)
- 人员与人脸底库作为核心业务资产，独立于算力插件基础设施，归属 `internal/person` 领域模块；
- `persons`：内部代理主键 `id`（服务端生成，不对外） + 对外业务键 `person_id` + 姓名 + 时间戳；
- **`person_id` 是外部系统关联人员的唯一凭据，因此必填、唯一、创建后不可修改**：API 层 `binding:"required"` + 变更返回 409 `PERSON_ID_IMMUTABLE`（不得静默忽略），数据库层 `NOT NULL CHECK (person_id <> '')` + UNIQUE 索引；
- `person_faces`：记录 1:N 人脸模板（一人多照）、所属人员的内部行 ID（列名 `person_id`，引用 `persons.id`，API 序列化为 `ownerRowId` 以区别于对外业务键）、照片存储路径、512 维特征向量二进制 `feature_vector`；
- 算法模块与 Runner 子进程仅通过 IPC 消息（`MSG_GALLERY_SYNC`, `MSG_GALLERY_UPSERT`, `MSG_GALLERY_DELETE`）接收特征向量更新，实现算力调度与业务资产严格解耦。底库身份恒取 `person_id` 原值，**不得回退到内部行 ID**，否则算法侧会混用两套标识符空间。

### R2. 算法包上传安全、多格式解压、结构规范与验包门禁 (Upload & Verification Gate)
- **上传传输与多格式解压安全 (Upload Security)**：
  - **流式落盘与大小限制**：支持 `multipart/form-data` 上传，通过流式写入临时文件，严禁一次性载入内存吃崩 RAM；单包体积硬限制为 500MB，超限立即 413 熔断；
  - **多格式魔数自动识别与解压 (Dual Format Support)**：
    - 支持 **`.tar.gz`**（前 2 字节魔数 `0x1F, 0x8B`）与 **`.zip`**（前 4 字节魔数 `0x50, 0x4B, 0x03, 0x04`，即 `PK..`）；
    - 兼容 Linux/CI 产物（`.tar.gz`）与 Windows 桌面打包产物（`.zip`），根据魔数自动分流解压；非法格式直接返回 `UNSUPPORTED_ARCHIVE_FORMAT` 拒绝；
  - **POSIX 权限自动补全**：针对 Windows 打包可能丢失 Linux 可执行位的情况，解压后自动修正 `plugin.so` 拥有 `0755` 权限，防止 `dlopen` 报权限拒绝；
  - **路径穿越防御 (Zip-Slip Protection)**：无论是 zip 还是 tar.gz，解压每个条目时必须使用 `filepath.Clean` 检查目标绝对路径，严禁条目包含 `../` 逃逸出临时解压根目录，发现即刻中断并报警；
  - **解压膨胀炸弹防御 (Decompression Bomb)**：增加计数读取器，解压后总文件体积超过 1GB 或文件数超过 1,000 个时强制中断并清理；
  - **原子暂存与 defer 清理**：上传与解压均在临时隔离目录执行，全部门禁通过后才原子移动至正式存储目录；任何一步失败（校验失败、超时、平台不匹配等），`defer` 保证 100% 抹除临时文件，零碎片残留。
- **约定优于配置的文件拓扑**：
  - 根目录约定的 `plugin.so`：入口动态库，无需在配置中声明文件路径；
  - 根目录约定的 `sample.jpg`：验包样本测试图；
  - 约定的 `models/` 目录：存放模型权重与标签映射；
  - 约定的 `.env`（可选）：算法包本地环境变量覆写文件，支持免重新编译动态调节底层推理驱动（如 RKNN/RGA/OpenMP）与模型超参数；
  - `manifest.json`：必须包含 `id`、`version`、`name`、`description`；明确技术形态 `task_type`（`detection`/`classification`）与业务分类 `category`（`safety`/`security`/`fire` 等）；声明平台契约 `platform: { arch, target }`；声明算力配额契约 `resources: { compute_cost, memory_mb }`（基于全设备 1,000 分基准算力池）；
  - `config.schema.json`（可选）：算法内部超参数（如置信度、NMS、过滤类别）的标准 JSON Schema，严格剥离抽帧率等平台级流控参数。若缺省则约定该算法为零参算法。
- **两阶段准入门禁与配额审计**：
  1. **第一阶段：静态契约预检**：
     - 上传解压后，Go 门禁服务首先比对 `platform.arch` 与 `platform.target` 是否与当前宿主硬件匹配，不一致秒级返回 `ALGO_PLATFORM_MISMATCH` 并清理临时目录；
     - 校验 `task_type` 与 `category` 枚举合法性，提取 `compute_cost` 与 `memory_mb`；
  2. **第二阶段：动态黑盒自测**：
     - 执行本地原生命令：
       ```bash
       zhulong_algo_runner --verify-plugin plugin.so --sample sample.jpg
       ```
     - 解析结构化 JSON 报告，核验 `plugin.so` 导出的 `result_type` 与 `manifest.json` 中的 `task_type` 绝对一致；
     - 退出码非 0 或报告状态为 `failed` 时强制回滚并清理，杜绝破损/不兼容插件污染系统。

### R3. 算法推理结果统一输出、人脸-人体绑定与动态底库热更新 (Inference Result & Gallery IPC)
- **四项黄金铁律**：
  1. **坐标归一化 `[0.0, 1.0]`**：所有检测框（人体框、人脸框）与关键点坐标必须是归一化浮点数，彻底解耦摄像头分辨率与播放器窗口拉伸；
  2. **单一语义化标签**：检测项仅包含 `box`、`label`、`score`，彻底消除冗余的 `class_id`，模型重新训练不影响上层业务；
  3. **时钟与帧保真 (PTS)**：输出事件强绑定输入帧的 `pts`，保障前端音画框精准同步；
  4. **零结果空数组约定**：无检测目标时严格返回空切片 `[]`，坚决不返回 `null`。
- **人脸-人体结构化绑定模型 (`task_type: "person_structure"`)**：
  - 采用**层次化 1:N 嵌套模型**：以 `Person` 为主体实体，包含必选 `body_box`（全身检测框）与**多个人脸挂载切片 `faces: []`**；
  - **支持多场景弹性拓扑**：
    - `faces: []`（空数组）：背对摄像头或严重遮挡，系统依然能够独立追踪人体轨迹；
    - `faces: [face1]`：标准单人常规状态；
    - `faces: [face1, face2]`：支持怀抱婴幼儿、背人、高度重叠拥挤等场景下一个人体框内挂载多个人脸；
  - 每个挂载的 `Face` 子对象内部完成**多帧时序特征融合（Temporal Feature Fusion）**、质量加权与轨迹平滑，直接输出已识别的人员身份（`matched`, `person_id`, `person_name`, `similarity`, `is_fused`）。
- **方式 B：IPC 动态底库热更新与「一人多照」注册模型 (Multi-Face Gallery Enrollment)**：
  - **一人多照特征注册模型 (1 Person : N Face Templates)**：
    - 支持同一人员注册多张登记照（如正脸、戴眼镜、不同发型/光照照）；
    - 每张登记照独立提取 512 维特征向量，统一归属于相同的 `person_id` 与 `person_name`；
  - **Top-K 候选人列表输出 (Top-K Candidates)**：
    - 算法在内部特征比对时，按人员 `person_id` 聚类去重取各人员最高置信度，输出前 $K$ 个最相似候选人列表（`candidates: [{ person_id, person_name, similarity }]`，按相似度降序）；
    - 根节点保留 Top-1 最佳匹配字段（`person_id`, `person_name`, `similarity`），同时提供 `candidates` 供人工审核研判、防近似脸混淆；
  - **集中底库管理**：Go 主进程在 SQLite 中集中管理人员档案主表与人脸特征明细表，对外暴露标准 Web REST API（录入人员、为人员追加人脸照片、删除单张人脸、注销人员）；
  - **UDS 动态消息同步**：Go 通过点对点 Unix Domain Socket 向 Runner 子进程下发底库同步消息（全量同步 `MSG_GALLERY_SYNC`、增量更新 `MSG_GALLERY_UPSERT`、删除单张/人员 `MSG_GALLERY_DELETE`）；
    - ⚠️ **未完成（NOT IMPLEMENTED）**：该 UDS 底库热更新链路**未实现**。Go 侧仅有消息构造函数（`algorithm/process.go` 的 `CreateGallerySyncMsg/Upsert/Delete`）与 `person.GetGallerySyncData`，**均无调用方**；native 侧 `ipc_protocol.hpp` **没有** gallery 消息类型；传输通道 / ACK 语义 / 分片策略均未定案。澄清结论见 `.trellis/tasks/10-12-algo-lifecycle-remediation/prd.md` R6 与 `design.md` §6。该缺口不属本任务范围，**不得**再标记为已完成。
  - **零重启秒级生效**：Runner 子进程接收后动态增删内存索引，在连续视频流中执行实时特征融合与比对，人员增删**全程不中断拉流与推理，零重启**。
- **业务事件模型决策（方案一：单目标独立事件，共享全景大图）**：
  - 同一帧内各人员生成独立的告警记录（`event_id`），拥有独立的生命周期、工单状态（待处理/已确认/误报）与自身全身/人脸特写抠图（`body_crop_url`、`face_crop_url`）；
  - 所有同一帧的目标共享同一个批次标识（`batch_id` / `frame_pts`）与同一个现场全景大图引用（`full_image_url`），全景图单次落盘，绝不冗余存储。
- **验包门禁断言**：`zhulong_algo_runner --verify-plugin` 在对测试样本进行自测时，门禁自动校验返回的所有坐标与置信度均在 `[0.0, 1.0]` 之间，且人脸框必须落在人体框拓扑几何内部，越界即刻判定验包失败。

### R4. 一包一进程看门狗守护 (Process Watchdog & Supervisor)
- 基于 Go `os/exec` 管理各算法包独立的 Runner 进程生命周期：
  ```bash
  zhulong_algo_runner --plugin <so_path> --broker <broker_sock> --name <pkg_name>
  ```
- 实时捕获子进程 `stdout`/`stderr` 日志至 Go 结构化日志，防止标准输出管道阻塞；
- 优雅关停时序：向子进程发送 `SIGTERM` ➔ 等待在途帧排空（超时 3 秒）➔ 超时未退则强发 `SIGKILL` ➔ 汇合回收 PID，杜绝僵尸进程。

### R5. 有界崩溃恢复状态机 (Bounded Auto-Recovery)
- 监听 Runner 子进程异常退出（`cmd.Wait()` 捕获非零退出码或信号触发）；
- **指数退避与预算窗口**：
  - 退避步长：1s ➔ 2s ➔ 4s ➔ 8s（上限 30s）；
  - 配额窗口：5 分钟内最多允许崩溃 3 次；
- **熔断保护 (Circuit Breaker)**：超出配额后，状态机将该包切换至 `CRASH_LOOP`（降级隔离），停止自动拉起并上报审计告警，彻底避免重启风暴吃满 CPU；
- **重置机制**：进程稳定运行超过 10 分钟，或管理员显式重新启用/更新后，清零退避状态与崩溃计数。

### R6. 包级安全重启升级与受控回滚 (Safe Upgrade & Rollback)
- **包级重启 (Package-level Restart)**：旧版本安全退出并释放共享内存租约后，再拉起新版本，宿主与其他算法包全程不重启、不中断；
- **就绪探测与失败回滚 (Rollback Gate)**：新版本启动后若在就绪窗口期（如 5 秒内）崩溃或握手失败，主控自动触发回滚逻辑，受控拉起上一个已知可用的备份版本；
- 若回滚版本亦不可用，立刻挂起并报警，严禁在两版本间来回无限震荡切换。

### R7. Uber Fx 生命周期装配与 RESTful API
- 在 `internal/algorithm/` 提供领域服务，在 `internal/app/app.go` 注册 `algorithmModule`；
- 提供 RESTful 接口：算法包列表、安装、更新、启用/停用、摄像头绑定管理与状态监控。

## 3. 验收标准 (Acceptance Criteria)

- [x] **AC1 (数据库与模型仓储 CRUD)**：SQLite 迁移执行成功，算法包元数据与摄像头绑定数据增删改查单元测试通过；人员底库域（`persons` / `person_faces`）一人多照 CRUD 单元测试通过，且 `person_id` 的**必填、唯一、不可变**三项契约均有回归覆盖（含 409 `PERSON_ID_ALREADY_EXISTS` / `PERSON_ID_IMMUTABLE` 映射）。
- [x] **AC2 (安装验包门禁拦截)**：上传流式落地受限在 500MB，支持 `.tar.gz` 与 `.zip` 双格式魔数自动识别与权限补全；拦截非法魔数文件与 Zip-Slip 路径穿越包；当上传破损、膨胀超限、平台不匹配或 ABI 不兼容算法包时，验包门禁成功拦截，临时文件被安全清理，不写入数据库；合法包顺利安装并录入系统。
- [x] **AC3 (Runner 进程监督与优雅退出)**：Supervisor 能正确拉起并监控 Runner 子进程，`Stop()` 时按 SIGTERM ➔ SIGKILL 优雅收敛，无僵尸进程残留。
- [x] **AC4 (有界退避与熔断防雪崩)**：连续注入子进程崩溃，状态机按照指数退避拉起；超出预算后停止重启并锁定为 `CRASH_LOOP`，调用恢复接口后重置。
- [x] **AC5 (升级失败受控回滚)**：模拟新版本插件启动即崩溃，系统自动终止新版本并顺利拉回旧版本，业务不中断；旧版同样失败时挂起并报警，不无限震荡。
- [x] **AC6 (并发安全与回归测试)**：Go vet、Go test -race 全部通过，系统健康检查与全量 smoke 测试通过。

## 4. 边界与范围外
- 本任务不涉及前端算法包管理 UI 界面（留给后续前端任务接入对应 RESTful API）；
- 本任务不涉及具体 NPU 业务算法模型的权重调优与识别业务规则处理。
