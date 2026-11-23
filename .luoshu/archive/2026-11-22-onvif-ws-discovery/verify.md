# Verify Contract: 2026-11-22-onvif-ws-discovery

> Propose 阶段确立的静态验收契约。Verify 阶段仅对照本契约与 `base_commit` diff 输出结论到 `verify-report.md`，严禁修改本契约或业务源码。

## 验收标准

- [ ] **V1：WS-Discovery 协议与 XML 编解码正确性**
  - 发送的 SOAP 报文包含正确的 Action (`http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe`) 与类型 (`dn:NetworkVideoTransmitter`)；
  - 能够正确反序列化标准 ONVIF IPC 应答的 `ProbeMatches`，提取出 `EndpointReference`、`XAddrs` 与空格分隔的 `Scopes`；
  - 正确解析出设备 IP、端口与经过 URL 解码的设备名称、厂商、型号；
  - 针对畸形 XML 报文或非目标应答平稳丢弃，不发生 panic。

- [ ] **V2：多网口有界探测与并发保护**
  - 组播探针自动向所有处于 `UP` 状态的有效非环回物理网口分发；
  - 探测过程受显式超时控制（默认 3s，最长不超过 10s），超时后立即停止监听并回收所有网络资源（连接和协程在 100ms 内收敛）；
  - 采用 singleflight 协同合并并发请求，短时间内重复请求直接复用探测结果，不产生并发组播风暴。

- [ ] **V3：HTTP API 契约与审计合规性**
  - 端点 `POST /api/v1/cameras/discover` 挂载在受 `auth.RequireAuth` 保护的路由组；
  - 成功时返回统一 JSON 信封 `{ code: "OK", message: "...", data: [...] }`；
  - 审计日志完整记录 `action: "camera_discover"`，Swagger 文档完整生成且通过 `make api-docs`。

- [ ] **V4：前端交互、表单回填与三语 i18n**
  - 摄像机主操作区提供“发现设备”入口，点击弹出设备发现对话框并可触发扫描；
  - 扫描中正确展示 Loading 与倒计时指示，扫描完成后展示设备卡片或表格；
  - 点击“以此设备添加”可自动打开摄像机配置弹窗，并将发现的设备名与 IP/推荐 RTSP URL 自动填充至表单；
  - `en`、`zh-Hans`、`zh-Hant` 语言包均包含所有新文案，无遗漏的未翻译 key；
  - 前端浏览器测试在真实 Chromium 下通过。

- [ ] **V5：负向边界（Negative Boundaries）**
  - **绝不破坏双流原子门禁**：通过发现回填创建摄像机时，依旧必须通过 `ValidateStreams` 校验主子流真实连通性与参数；
  - **绝不增加 C++ 原生引擎负担**：整个发现能力在 Go 宿主层完成，C++ 引擎与 C ABI 零改动；
  - **绝不引入盲目全网段 TCP/SYN 端口扫描**；
  - **无已存接口破坏或数据库变更**：不改变 `cameras` / `camera_streams` 的现有表结构，不增加破坏性迁移。

## 验证门禁与针对性测试命令

```bash
# 1. 发现模块针对性单元测试 (带 -race)
go test -race ./internal/camera/discovery -v

# 2. 摄像机业务层集成测试 (带 -race)
go test -race ./internal/camera -run 'TestCameraHandlerDiscover|TestHandler_Discover' -v

# 3. 前端 API 与组件测试 (真实浏览器环境)
cd web && pnpm test -- src/features/cameras/api/cameras-api.test.ts src/features/cameras/components/cameras-discover-dialog.test.tsx

# 4. API 文档生成检查
make api-docs

# 5. 全量静态质量门禁
make check
```
