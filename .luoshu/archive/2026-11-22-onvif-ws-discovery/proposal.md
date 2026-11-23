# Proposal: 2026-11-22-onvif-ws-discovery

## 背景与目标

Zhulong 作为本地优先的边缘 NVR 与 AI 运维宿主，现阶段接入 IP 摄像机需要用户完全手动输入 RTSP URL（包括复杂的私有厂商路径与端口）。在现场部署多台摄像机的边缘场景下，运维人员需查阅说明书逐台配置，门槛高且极易出错。

本变更旨在为系统引入符合安防行业国际标准的 **ONVIF WS-Discovery 局域网设备发现能力**：
在 Go 宿主层通过纯 Go 标准库实现标准 SOAP over UDP 组播探测，收集局域网内广播响应的 IPC 设备信息（IP、厂商、型号、端点地址），并在 Web 控制台摄像机管理模块提供可视化扫描面板与一键回填能力，将摄像机添加流程从“盲目手工拼 URL”进化为“一键发现、自动填入、原子探测入库”。

---

## In Scope（本次包含）

1. **Go 后端 ONVIF WS-Discovery 协议与探测引擎 (`internal/camera/discovery`)**：
   - 构造符合 WS-Discovery 规范的 SOAP Probe XML 报文（类型 `dn:NetworkVideoTransmitter`）；
   - 基于 UDP 多播（`239.255.255.250:3702`）向可用网络接口发送探针，并在绝对超时预算内（默认 3s，上限 10s）接收单播 `ProbeMatches`；
   - 解析 XML 响应中的 `EndpointReference` (UUID)、`XAddrs` (服务地址)、`Scopes` (提取硬件品牌、型号、设备名) 与设备 IPv4 地址；
   - 适配多网口环境：枚举非环回、活动状态的物理网络接口分别广播，并以设备 UUID + IP 进行去重；
   - 并发与防刷控制：采用 `singleflight` 防止密集并发触发网络组播风暴。
2. **HTTP API 与审计契约 (`internal/camera`)**：
   - 新增 `POST /api/v1/cameras/discover` 端点，挂载于受 `auth.RequireAuth` 保护的 `protected_routes` 路由组；
   - 统一遵循 `{ code: "OK", message: "...", data: [...] }` 响应信封；
   - 补齐 Swagger 注解与 audit 审计日志标签；
   - 错误国际化映射（`internal/httputil/locale.go` / `internal/camera/locale.go`）。
3. **前端设备发现与一键回填交互 (`web/src/features/cameras`)**：
   - API 客户端方法 `camerasApi.discover(timeoutSeconds)` 与 Zod 响应 schema 校验；
   - 在摄像机操作区域增加“发现设备 / Discover”入口与候选设备弹窗/抽屉；
   - 列表化展示发现的设备（IP、厂商、型号、UUID、ONVIF 地址）；
   - 支持“以此设备添加”操作，自动将选中的设备名称与 IP 预填至新增摄像机表单的 `name` 与 `mainRtspUrl` 中；
   - 完整覆盖 `en` / `zh-Hans` / `zh-Hant` 国际化文案。
4. **自动化测试**：
   - Go 侧单测：WS-Discovery SOAP XML 编码/解码测试、模拟 UDP 响应解析测试、Handler 路由与防刷单飞测试；
   - 前端真实 Chromium 浏览器单测：扫描触发状态、设备列表渲染与表单回填交互测试。

---

## Out of Scope（明确不做）

1. **盲目 TCP/SYN 端口扫描**：坚决不实现针对局域网子网的 IP/Port 爆破扫描（避免触发网络安全警报或导致嵌入式 IPC 网络栈崩溃）。
2. **ONVIF 完整客户端协议栈**：本次不实现 ONVIF PTZ 云台控制、设备参数修改、固件升级、时钟同步或事件订阅；聚焦于设备发现与流地址预填。
3. **绕过双流门禁**：发现设备并回填 RTSP 后，创建摄像机依然严格经由现存的 `ProbeService.ValidateStreams` 原子双流探测门禁，不为发现设备开放任何跳过校验的后门。
4. **Native C++ 引擎改动**：设备扫描纯属网络层与控制面业务，保持零 CGO、零 Native 改动。

---

## 影响范围（模块 / 接口契约 / Specs & Rules）

- **后端包**：`internal/camera`、`internal/camera/discovery`（新增子包）、`internal/app`（端点接线与依赖注入）。
- **HTTP API**：新增 `POST /api/v1/cameras/discover`。
- **前端特性**：`web/src/features/cameras`（API、Schema、组件、i18n 资源文件）。
- **规范与规则对齐**：
  - `.luoshu/specs/architecture.md`：维持 Go 宿主与 Native Engine 的清晰职责分界。
  - `.luoshu/specs/backend/camera-guidelines.md`：维护摄像机凭据安全与原子双流门禁。
  - `.luoshu/rules/go-host/http-contract.md`：统一响应信封、统一审计、Swagger 注册。
  - `.luoshu/rules/go-host/concurrency-and-errors.md`：带绝对超时的 UDP 监听、防止孤儿 goroutine。
  - `.luoshu/rules/web/frontend-api-boundary.md`：Zod 运行时校验外部数据、解包规范。
