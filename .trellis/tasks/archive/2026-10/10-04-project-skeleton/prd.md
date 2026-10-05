# 项目骨架初始化

## Goal

建立 Zhulong 后续功能开发可依赖、可验证的全栈工程起点，落实既有产品 PRD 确认的 Go 宿主、React 控制台与 C++ 原生引擎边界。

## Confirmed Background and Decisions

- 当前仓库没有应用源码、前端工程、原生引擎构建文件或应用测试。
- 仓库已有未跟踪的 `AGENTS.md`、`docs/prd/prd-v1.0.md` 和 `go.mod`；模块路径为 `github.com/nikonikowuw/Zhulong`，Go 版本为 `1.27.1`。
- 用户已确认按产品 PRD 的第一阶段范围同时搭建 Go 宿主、React/Vite 内嵌前端及最小 C ABI/C++ Pipeline 生命周期。
- 产品 PRD 指定 Gin、Fx、GORM/SQLite 版本化迁移、Zap、Swagger，以及 React、严格 TypeScript、Vite、React Router、TanStack Query 等既定架构；实现细节仍以设计文档收敛。
- 当前开发环境为 macOS arm64、Go 1.27.1、Node 24.15.0、npm 11.12.1、CMake 4.4.0、Apple Clang 17.0.0；这只是本机验证基线，不代表产品支持平台承诺。
- 首发操作系统、目标板卡、NPU/GPU、厂商 SDK、媒体协议和模型均未选定。
- 用户已确认 API 响应信封含必需的 `code`、`message`、`data`，并在 HTTP 422 字段校验错误时允许可选 `details`；错误响应的 `data` 为 `null`，details 包含字段路径、稳定错误码和本地化消息，不含原始输入。该合同需同步至产品 PRD 和后端规范。

## In Scope

- 可构建、可启动的 Go 宿主及 Fx 生命周期装配，采用 Gin、GORM/SQLite、版本化 SQL 迁移、Zap 和 Swagger。
- React/Vite TypeScript 前端最小应用，可构建为静态资源并由 Go 二进制内嵌托管。
- 纯 C ABI、不透明引擎句柄、CGO 桥接和可在无硬件条件下验证的 C++ 引擎生命周期骨架。
- 根级可重复构建、测试和运行入口，以及足以让新开发者在本机复现的工程说明。
- 用健康检查和路由行为验证 Go API、嵌入式 SPA 与原生层集成。

## Out of Scope

- 摄像机接入、RTSP/ONVIF、编解码、录像、推理、业务告警和证据管理。
- 任何厂商 NPU/GPU SDK、具体目标硬件适配、性能/精度承诺或 Linux BSP 兼容声明。
- 完整产品工作流、生产部署拓扑和未选定的媒体、模型、安全及数据保留策略。

## Requirements

- 工程命令须能从仓库根目录按文档执行，不依赖未说明的手工生成步骤。
- 前端生产资源须由 Go 嵌入并通过 Go 宿主提供；API 未匹配路由不得返回 SPA HTML。
- Go/CGO/C++ 仅通过稳定纯 C ABI 与不透明句柄交互，明确分配释放责任，C++ 异常不得越过 ABI。
- 应用启动/停止顺序须满足数据库迁移先于对外 HTTP 服务、停止 HTTP 后释放原生和数据库资源。
- 不把无硬件运行的生命周期骨架描述成已具备视频处理或硬件加速能力。

## Acceptance Criteria

- [ ] 从干净检出按仓库文档执行根级构建命令，可生成包含前端资源和原生库的 Go 应用。
- [ ] 根级验证命令覆盖 Go、C++/C ABI、前端各自的自动化检查，并可重复运行。
- [ ] 应用可启动；健康检查成功；根路由返回内嵌 SPA；未匹配 API 路由返回 JSON 错误而非 SPA 页面。
- [ ] C ABI 引擎句柄的创建、启动、停止、销毁可在无目标硬件环境下通过测试；所有 ABI 导出函数隔离 C++ 异常。
- [ ] 关闭顺序和迁移失败阻断服务启动均有测试或等效验证。
- [ ] 文档明确本机验证平台与未支持/待验证的目标硬件边界。
- [ ] 响应体包含必需的 `code`、`message`、`data`；错误时 `data: null`，HTTP 422 字段校验错误可带本地化的 `details`，且不会泄漏原始输入或内部错误。
- [ ] 产品 PRD、后端 HTTP/错误规范、Swagger 与实现遵守相同响应合同。

## Deferred Decisions

- 首发操作系统、CPU 架构、目标板卡和厂商硬件 SDK 仍待后续目标平台决策，不影响本机无硬件骨架验收。
