# Go 后端目录架构规范

> 模块化分包架构、各目录职责及包间单向依赖规则。

---

## 1. 目录结构 (Directory Layout)

> ⚠️ 下列「已实现」部分必须与代码保持一致；「规划中」部分为指导后续实现的约定，**功能未开始前严禁创建空目录占位**。

```text
cmd/
  Zhulong/main.go             # 极简启动入口 (调用 internal/app 启动)
internal/
  app/                        # 应用全局装配、Fx 模块与生命周期编排 (app.go, router.go, runtime.go, adapters.go)
  apidocs/                    # swag 生成的 Swagger 产物 (由 make api-docs 覆盖，禁止手改)
  apperr/                     # 传输无关的领域语义错误 (Kind + 机器码 + cause)
  audit/                      # 操作与安全审计日志核心域 (无 HTTP/Gin 依赖)
  audit/http/                 # 审计的 Gin 适配层 (package audithttp: handler.go, scope.go, panic.go)
  auth/                       # 单用户认证、内存 Session 与登录限流
  apitoken/                   # 机器间 OpenAPI Bearer Token 生成、哈希存储与鉴权中间件
  openapi/                    # 面向第三方平台的 M2M OpenAPI 端点与上传并发限流器
  camera/                     # 相机管理业务模块 (主子流建模、AES-GCM 凭据加密、探活调度、StreamHub)
  gb28181/                    # GB/T 28181 SIP 信令、Digest 认证、目录同步与通道转相机
  person/                     # 人员与人脸底库域 (persons / person_faces 一人多照 CRUD 与以图搜图)
  faceextract/                # 离线单张人脸特征提取客户端 (通过子进程标准流协议对接算法包)
  algorithm/                  # 算法包生命周期与静态资产管理中心 (验包解包、Manifest 与版本元数据)
  algorunner/                 # 算法子进程看护与自愈调度 (Supervisor 守护、退避熔断、Runtime 磁盘布局)
  algoipc/                    # 算法 Runner 子进程间二进制 IPC 编解码与定长帧头协议
  aitypes/                    # 纯数据契约包 (InferenceResult, DetectionItem, 任务与分类模型)
  region/                     # 区域策略纯逻辑包 (ROI/MASK 归一化矩形校验与分析窗口 crop)
  schedule/                   # 时间调度模板校验与布防时段求值纯逻辑包
  task/                       # 视频分析任务管理域 (Camera 与 Algorithm 绑定、调度与区域配置缓存)
  asyncjob/                   # 持久化后台异步任务队列与重启孤儿恢复 (导出/批量处理等)
  record/                     # 感知记录与结构化报警持久化域 (含 SnapshotStore 抓拍落盘与保留清理)
  alertpush/                  # 告警多目标推送域 (Webhook / MQTT、过滤、HMAC-SHA256 签名媒体 URL)
  spatial/                    # 2D 空间画布底图、相机点位标定与人员轨迹聚合域
  storage/                    # 边缘媒体存储与动态生命周期 (路径规范、statfs 水位感知、回差清理)
  mediafile/                  # 媒体文件只读字节层 (按相对路径取元信息与 Range 传输，无列表)
  network/                    # 异构宿主网络配置与两阶段回滚看门狗模块
  systemtime/                 # 时间同步 (NTP) 与系统时区配置模块
  systeminfo/                 # 宿主硬件遥测 (CPU/NPU/内存/网口过滤) 与设备身份配置域
  engine/                     # Go 侧 CGO 驱动桥接包 (唯一 import "C"，隔离 C ABI，无业务逻辑)
  nativelog/                  # 有界非阻塞 Native 日志通道 (隔离实时 C++ 线程与磁盘/stderr 写 I/O)
  database/                   # SQLite 连接初始化与版本化迁移执行器 (含 embedded migrations/)
  webui/                      # 内嵌前端静态资源托管 (embed.FS 与 Gin 静态托管中间件)
  httpmiddleware/             # 通用 API 中间件 (Recovery, RequestID, AccessLog, CORS, MaxBodyLimit, RateLimit)
  httputil/                   # HTTP 共享工具包 (统一响应信封、泛型端点适配器、分页、i18n 与错误映射)
  clientlog/                  # 前端日志安全上报端点与字段脱敏桥接
  idutil/                     # ULID 分布式主键生成器 (保证 SQLite B-Tree 顺序追加)
  logctx/                     # 轻量 Zap 上下文传递 (requestId / traceId / streamId，无 HTTP 依赖)
  logsafe/                    # 零业务依赖的敏感字段与 URL/凭据脱敏库
native/                       # C++ 流水线源码、SDK 与 C ABI (独立于 Go 源码树)
web/                          # React 前端工程根目录

# 规划中（尚未创建）
internal/
  recording/                  # 连续视频录像与切片存储数据面
```

> **提示**：新增带 `@Router` 注解的业务包后，须同步加入根目录 `Makefile` 的 `api-docs --dir` 显式白名单，否则 Swagger 文档会静默漏掉对应端点。

---

## 2. 依赖方向与单向流转铁律

```txt
cmd/Zhulong ➔ internal/app ➔ 全量领域模块、中间件与基础设施装配
  业务域单向依赖：
    internal/camera      ➔ internal/{aitypes, auth, engine, region, idutil, ...}
    internal/gb28181     ➔ internal/camera   # 只向 camera 下游创建/删除通道，camera 不知 gb28181
    internal/algorithm   ➔ internal/{aitypes, algorunner, task}
    internal/algorunner  ➔ internal/algoipc
    internal/faceextract ➔ internal/algorunner
    internal/task        ➔ internal/{region, schedule, idutil}
    internal/region      ➔ internal/aitypes
    internal/person      ➔ internal/{asyncjob, idutil}
    internal/openapi     ➔ internal/{apitoken, person, httpmiddleware}
    internal/alertpush   ➔ internal/{mediafile, record, logsafe, httpmiddleware, idutil}
    internal/record      ➔ internal/{aitypes, engine, idutil}
    internal/mediafile   ➔ internal/{storage, httputil, apperr}   # 严禁 import internal/camera（防拖入 CGO engine）
  横切基础设施单向依赖：
    internal/engine         ➔ internal/{algoipc, nativelog}
    internal/audit/http     ➔ internal/{audit, httputil}
    internal/httpmiddleware ➔ internal/{apperr, httputil, logctx, logsafe}
    internal/httputil       ➔ internal/{apperr, logctx}
    internal/clientlog      ➔ internal/{httpmiddleware, httputil, logsafe}
    零业务内部依赖基础包：aitypes, algoipc, apperr, database, idutil, logctx, logsafe, nativelog, schedule, webui
```

1. **禁止反向依赖**：业务模块严禁导入 `internal/app`。
2. **中间件无业务偏见**：`internal/httpmiddleware` 仅承载通用 HTTP 管道行为，可依赖 `internal/httputil`、`apperr`、`logctx`、`logsafe`，严禁反向依赖 `internal/camera` 等上层业务模块。
3. **禁止循环依赖**：业务模块之间严格禁止形成导入环路（A ➔ B ➔ A）；跨模块交互由 `internal/app` 适配器或窄接口解耦。
4. **底层基础无业务偏见**：`engine`、`database`、`httputil`、`apperr`、`idutil`、`logctx`、`logsafe`、`nativelog` 严禁反向导入任何上层业务模块。
5. **禁止根公共库 `pkg/`**：单机程序私有实现收敛在 `internal/`，不暴露外部库。
6. **C++ 类型不出 engine 包**：`internal/engine` 必须将所有 C 类型（`C.int`, `unsafe.Pointer`）转为 Go 原生类型，业务模块严禁 `import "C"`。
7. **审计核心无 HTTP 偏见**：`internal/audit` 不得导入 Gin 或 `httputil`；Gin 中间件、Tag API 与审计 Handler 全部位于 `internal/audit/http`（包名 `audithttp`）。`audit/http` 不得导入 `internal/auth` 等业务包，避免 `auth ➔ audit/http ➔ auth` 环路。
8. **审计 API 仅 Handler 层调用**：Service 层不得持有 `*gin.Context`、不得导入审计包。用户操作的审计声明归属 HTTP Handler；后台事件仅可通过只含 `RecordSystem` 的窄接口记录（如 `systemtime.SystemEventRecorder`）。
9. **`internal/mediafile` 禁止 import `internal/camera`**：`camera` 间接依赖 `internal/engine`（CGO），一旦引入，该包及其测试将被迫走 native wrapper。它只依赖 `internal/storage`（取媒体根配置与文件系统遥测）、`httputil`、`apperr`，因此可裸跑 `go test -race`。
