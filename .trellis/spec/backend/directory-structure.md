# Go 后端目录架构规范

> 模块化分包架构、各目录职责及包间单向依赖规则。

---

## 1. 目录结构 (Directory Layout)

> ⚠️ 下列「已实现」部分必须与代码保持一致；「规划中」部分为指导后续实现的约定，**功能未开始前严禁创建空目录占位**。

```text
cmd/
  Zhulong/main.go             # 极简启动入口 (调用 internal/app 启动)
internal/
  app/                        # 应用全局装配与 Fx 生命周期 (app.go, router.go, runtime.go)
  apidocs/                    # swag 生成的 Swagger 产物 (由 make api-docs 覆盖，禁止手改)
  audit/                      # 操作与安全审计日志模块 (model.go, actor.go, store.go, service.go)
  audit/http/                 # 审计的 Gin 适配层 (package audithttp: handler.go, scope.go, panic.go)
  auth/                       # 单用户认证与会话管理 (handler.go, store.go, limiter.go, middleware.go)
  camera/                     # 相机管理业务模块 (主子流建模、凭据加密、探活调度)
  person/                     # 人员与人脸底库域 (persons / person_faces 一人多照 CRUD)
  algorithm/                  # 算法包生命周期、验包门禁、Runner 进程监督与自愈回滚
  algoipc/                    # 算法 Runner 子进程间二进制 IPC 编解码与定长帧头协议
  aitypes/                    # 纯数据契约包 (InferenceResult, DetectionItem, 任务与分类模型)
  task/                       # 视频分析任务管理域 (Camera 与 Algorithm 动态绑定、配置与帧率调度)
  record/                     # 感知记录与结构化报警持久化域
  storage/                    # 边缘媒体存储与动态生命周期 (路径规范、statfs 水位感知)
  mediafile/                  # 媒体文件只读字节层 (按相对路径取元信息与 Range 传输，无列表)
  network/                    # 异构宿主网络配置与两阶段回滚看门狗模块
  systemtime/                 # 时间同步 (NTP) 与系统时区配置模块
  engine/                     # Go 侧 CGO 驱动桥接包 (隔离 C ABI，无业务逻辑)
  database/                   # SQLite 连接初始化与迁移执行器 (含 embedded migrations/)
  webui/                      # 内嵌前端静态资源托管 (embed.FS 与 Gin 静态托管中间件)
  httpmiddleware/             # API 中间件 (Recovery, RequestID, AccessLog, CORS, MaxBodyLimit)
  httputil/                   # HTTP 共享工具包 (统一响应信封、分页、i18n 与错误辅助函数)
native/                       # C++ 流水线源码与 C ABI (独立于 Go 源码树)
web/                          # React 前端工程根目录

# 规划中（尚未创建）
internal/
  recording/                  # 视频录像与切片存储管理模块
```

---

## 2. 依赖方向与单向流转铁律

```txt
cmd/Zhulong ➔ internal/app ➔ internal/{auth, camera, person, algorithm, task, record, storage, mediafile, network, systemtime, audit} ➔ internal/{engine, database, httputil}
                             internal/app ➔ internal/httpmiddleware ➔ internal/httputil
                             internal/app ➔ internal/audit/http ➔ internal/audit
                             internal/mediafile ➔ internal/{storage, httputil, apperr}   # 禁止反向；严禁 import internal/camera（间接拖入 CGO engine）
```

1. **禁止反向依赖**：业务模块严禁导入 `internal/app`。
2. **中间件无业务偏见**：`internal/httpmiddleware` 仅承载通用 HTTP 管道行为，可依赖 `internal/httputil`，严禁反向依赖 `internal/camera` 等上层业务模块。
3. **禁止循环依赖**：业务模块之间严格禁止形成导入环路（A ➔ B ➔ A）；跨模块交互由上层事件或小接口解耦。
4. **底层基础无业务偏见**：`engine`, `database`, `httputil` 严禁反向导入任何上层业务模块。
5. **禁止根公共库 `pkg/`**：单机程序私有实现收敛在 `internal/`，不暴露外部库。
6. **C++ 类型不出 engine 包**：`internal/engine` 必须将所有 C 类型（`C.int`, `unsafe.Pointer`）转为 Go 原生类型，业务模块严禁 `import "C"`。
7. **审计核心无 HTTP 偏见**：`internal/audit` 不得导入 Gin 或 `httputil`；Gin 中间件、Tag API 与审计 Handler 全部位于 `internal/audit/http`（包名 `audithttp`）。`audit/http` 不得导入 `internal/auth` 等业务包，避免 `auth ➔ audit/http ➔ auth` 环路。
8. **审计 API 仅 Handler 层调用**：Service 层不得持有 `*gin.Context`、不得导入审计包。用户操作的审计声明归属 HTTP Handler；后台事件仅可通过只含 `RecordSystem` 的窄接口记录（如 `systemtime.SystemEventRecorder`）。
9. **`internal/mediafile` 禁止 import `internal/camera`**：`camera` 间接依赖 `internal/engine`（CGO），一旦引入，该包及其测试将被迫走 native wrapper。它只依赖 `internal/storage`（取媒体根配置与文件系统遥测）、`httputil`、`apperr`，因此可裸跑 `go test -race`。
