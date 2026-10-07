# Go 后端目录架构规范

> 模块化分包架构、各目录职责及包间单向依赖规则。

---

## 1. 规划目录结构（Proposed Layout）

> ⚠️ 以下为指导后续实现的规划约定，功能未开始前严禁创建空目录占位。

```text
cmd/
  Zhulong/main.go               # 极简启动入口 (调用 internal/app 启动)
internal/
  app/                        # 应用全局装配与 Fx 生命周期 (app.go, lifecycle.go)
  audit/                      # 操作与安全审计日志模块 (model.go, store.go, service.go, handler.go)
  auth/                       # 单用户认证与会话管理 (handler.go, store.go, limiter.go)
  camera/                     # 相机管理业务模块 (camera.go, handler.go, store.go, *_test.go)
  recording/                  # 视频录像与切片存储管理模块
  inference/                  # AI 分析任务配置与检测规则管理模块
  engine/                     # Go 侧 CGO 驱动桥接包 (隔离 C ABI，无业务逻辑)
  database/                   # SQLite 连接初始化与迁移执行器 (含 embedded migrations/)
  webui/                      # 内嵌前端静态资源托管 (embed.FS 与 Gin 静态托管中间件)
  middleware/                 # 全局与 API 路由中间件 (RequestID, AccessLog, Recovery, CORS)
  httputil/                   # HTTP 共享工具包 (统一响应信封与错误辅助函数)
native/                       # C++ 流水线源码与 C ABI (独立于 Go 源码树)
web/                          # React 前端工程根目录
```

---

## 2. 依赖方向与单向流转铁律

```txt
cmd/Zhulong ➔ internal/app ➔ internal/{camera, recording, inference} ➔ internal/{engine, database, httputil}
                             internal/app ➔ internal/middleware ➔ internal/httputil
```

1. **禁止反向依赖**：业务模块严禁导入 `internal/app`。
2. **中间件无业务偏见**：`internal/middleware` 仅承载通用 HTTP 管道行为，可依赖 `internal/httputil`，严禁反向依赖 `internal/camera` 等上层业务模块。
3. **禁止循环依赖**：业务模块之间严格禁止形成导入环路（A ➔ B ➔ A）；跨模块交互由上层事件或小接口解耦。
4. **底层基础无业务偏见**：`engine`, `database`, `httputil` 严禁反向导入任何上层业务模块。
5. **禁止根公共库 `pkg/`**：单机程序私有实现收敛在 `internal/`，不暴露外部库。
6. **C++ 类型不出 engine 包**：`internal/engine` 必须将所有 C 类型（`C.int`, `unsafe.Pointer`）转为 Go 原生类型，业务模块严禁 `import "C"`。
