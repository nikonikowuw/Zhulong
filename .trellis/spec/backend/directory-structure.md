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
  audit/                      # 操作与安全审计日志模块 (model.go, store.go, service.go, handler.go)
  auth/                       # 单用户认证与会话管理 (handler.go, store.go, limiter.go, middleware.go)
  camera/                     # 相机管理业务模块 (主子流建模、凭据加密、探活调度)
  person/                     # 人员与人脸底库域 (persons / person_faces 一人多照 CRUD)
  algorithm/                  # 算法包生命周期、验包门禁、Runner 进程监督与自愈回滚
  storage/                    # 边缘媒体存储与动态生命周期 (路径规范、statfs 水位感知)
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
cmd/Zhulong ➔ internal/app ➔ internal/{auth, camera, person, algorithm, storage, network, systemtime, audit} ➔ internal/{engine, database, httputil}
                             internal/app ➔ internal/httpmiddleware ➔ internal/httputil
```

1. **禁止反向依赖**：业务模块严禁导入 `internal/app`。
2. **中间件无业务偏见**：`internal/httpmiddleware` 仅承载通用 HTTP 管道行为，可依赖 `internal/httputil`，严禁反向依赖 `internal/camera` 等上层业务模块。
3. **禁止循环依赖**：业务模块之间严格禁止形成导入环路（A ➔ B ➔ A）；跨模块交互由上层事件或小接口解耦。
4. **底层基础无业务偏见**：`engine`, `database`, `httputil` 严禁反向导入任何上层业务模块。
5. **禁止根公共库 `pkg/`**：单机程序私有实现收敛在 `internal/`，不暴露外部库。
6. **C++ 类型不出 engine 包**：`internal/engine` 必须将所有 C 类型（`C.int`, `unsafe.Pointer`）转为 Go 原生类型，业务模块严禁 `import "C"`。
