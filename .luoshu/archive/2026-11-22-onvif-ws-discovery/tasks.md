# Tasks: 2026-11-22-onvif-ws-discovery

> 必须按自底向上依赖拓扑顺序排列，严禁前向依赖。每个任务需标注前置依赖并注明增量验证命令。

## 1. 实施任务

- [x] 1.1 (deps: none) 定义 ONVIF WS-Discovery 数据模型与 SOAP Probe/ProbeMatches XML 编解码契约 — `internal/camera/discovery/model.go`, `internal/camera/discovery/soap.go`, `internal/camera/discovery/soap_test.go` (check: `go test -race ./internal/camera/discovery -run TestSOAP`)
- [x] 1.2 (deps: 1.1) 实现多网口枚举、UDP 组播探针发送与有界单播响应收集引擎 — `internal/camera/discovery/discoverer.go`, `internal/camera/discovery/discoverer_test.go` (check: `go test -race ./internal/camera/discovery -run TestDiscoverer`)
- [x] 1.3 (deps: 1.2) 在 Camera 业务域集成设备发现服务与 HTTP 控制器 — `internal/camera/requests.go`, `internal/camera/responses.go`, `internal/camera/service.go`, `internal/camera/handler.go`, `internal/camera/handler_test.go`, `internal/camera/locale.go` (check: `go test -race ./internal/camera -run TestCameraHandlerDiscover`)
- [x] 1.4 (deps: 1.3) 将发现端点挂载至受保护路由组、补充审计动作并更新 OpenAPI/Swagger 文档 — `internal/audit/audit.go`, `internal/app/router.go`, `internal/camera/handler.go` (check: `make api-docs && make go-check`)
- [x] 1.5 (deps: 1.4) 前端 API 封装、Zod 响应校验与客户端单元测试 — `web/src/features/cameras/data/schema.ts`, `web/src/features/cameras/api/cameras-api.ts`, `web/src/features/cameras/api/cameras-api.test.ts` (check: `cd web && pnpm test -- src/features/cameras/api/cameras-api.test.ts`)
- [x] 1.6 (deps: 1.5) 前端摄像机发现对话框、表单回填交互与多语言文案覆盖 — `web/src/features/cameras/components/cameras-discover-dialog.tsx`, `web/src/features/cameras/components/cameras-discover-dialog.test.tsx`, `web/src/features/cameras/components/cameras-action-dialog.tsx`, `web/src/features/cameras/components/cameras-primary-buttons.tsx`, `web/src/features/cameras/locales/{en,zh-Hans,zh-Hant}.json` (check: `cd web && pnpm test -- src/features/cameras/components/cameras-discover-dialog.test.tsx`)
- [x] 1.7 (deps: 1.6) 更新摄像机领域规格规范与全量门禁检查 — `.luoshu/specs/backend/camera-guidelines.md` (check: `make check`)
