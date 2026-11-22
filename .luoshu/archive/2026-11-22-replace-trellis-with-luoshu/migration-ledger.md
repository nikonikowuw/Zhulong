# Trellis → 洛书迁移台账

来源基准：`git ls-files .trellis/spec`。下列证据路径相对于仓库根；旧文档原位保留，仅供历史追溯。并入表示按当前实现重述工程契约，不表示逐字导入。

## 根、前端与跨层指南

| 旧路径 | 处置 | 当前证据与取舍 |
| --- | --- | --- |
| `.trellis/spec/index.md` | 洛书规格索引取代 | `.luoshu/specs/index.md` 路由到架构及五域；不复制 Trellis 开发清单。 |
| `.trellis/spec/architecture.md` | 并入架构及五域 | `internal/app/router.go:134-235`、`internal/app/runtime.go:527-574`、`native/CMakeLists.txt:131-160,348-366`，保留单宿主与独立算法进程边界。 |
| `.trellis/spec/naming-guidelines.md` | 有契约的部分并入各域 | `internal/camera/handler.go` 按 HTTP 投影角色命名；`web/src/hooks/use-table-url-state.ts` 等使用 kebab-case，不导入普遍 PascalCase/camelCase 文件名规定。 |
| `.trellis/spec/frontend/index.md` | 并入 Web 规格和索引 | `web/package.json`、`web/vite.config.ts:37-101`；不重复框架版本矩阵。 |
| `.trellis/spec/frontend/directory-structure.md` | 并入 Web；提炼路由规则 | `web/src/routes/_authenticated/persons/index.tsx:1-8` 引入页面入口和 search schema，路由层不是业务实现；不把入口原则写成不存在的绝对限制。 |
| `.trellis/spec/frontend/development-guidelines.md` | 并入 Web | `web/src/hooks/use-table-url-state.ts:21-36,79-95`；`web/src/main.tsx:54-88`；`web/src/features/live/`。保留 URL、错误反馈、实时媒体边界，不复制临时页面样式范本。 |
| `.trellis/spec/frontend/quality-guidelines.md` | 并入 Web；提炼测试规则 | `Makefile:60-66,250-256`、`web/vite.config.ts:56-101`。实际 Vitest 浏览器项目，不将历史端口竞争归因为本次失败。 |
| `.trellis/spec/frontend/theme-and-i18n.md` | 并入 Web；保留语义 token、方向与三语 | `web/src/styles/theme.css`、`web/src/context/direction-provider.tsx`、`web/src/lib/i18n.ts:60-158`；不重复固定色值/截图式范本。 |
| `.trellis/spec/guides/index.md` | 不重复迁入 | 架构和领域索引接管跨层导航；此文件为旧指南导航。 |
| `.trellis/spec/guides/code-reuse-thinking-guide.md` | 通用方法论不迁；复用点并入域契约 | `internal/httputil/response.go:16-37`、`web/src/lib/api-client.ts:71-109` 已提供实际公共边界；不复制泛用抽象清单。 |
| `.trellis/spec/guides/cross-layer-thinking-guide.md` | 并入架构、Web、原生与 SDK | `internal/engine/engine.go:8-29`；`internal/algorunner/gallery.go:39-53,124-132`、`internal/algorunner/gallery_test.go:132-240` 按编码后的字节预算分片，不按条数。 |

## Go 与数据

| 旧路径 | 处置 | 当前证据与取舍 |
| --- | --- | --- |
| `.trellis/spec/backend/index.md` | 洛书索引取代 | `internal/app/app.go:1153-1185`、`Makefile:241-256`；不复制后端入口。 |
| `.trellis/spec/backend/directory-structure.md` | 并入 Go | `internal/record/module.go:68`、`internal/record/handler.go:22`、`internal/record/store.go:48-115`；不复制包树。 |
| `.trellis/spec/backend/dependency-injection.md` | 并入 Go 与 Fx 规则 | `internal/app/runtime.go:282-421,527-574`、`internal/app/runtime_test.go:355`；保留共享回滚。 |
| `.trellis/spec/backend/host-runtime-contract.md` | 并入 Go 与架构 | `internal/app/router.go:255-308`、`internal/app/runtime.go:479-574`；区分就绪和清理义务。 |
| `.trellis/spec/backend/http-api-guidelines.md` | 并入 Go 与 HTTP 规则 | `internal/httputil/errors.go:105-189`、`internal/app/router.go:134-235`、`Makefile:241-242`；clientlog 注解当前不在生成白名单。 |
| `.trellis/spec/backend/authentication-guidelines.md` | 并入 Go 与鉴权规则 | `internal/auth/cookie.go:14-21,47-61`、`internal/apitoken/middleware.go:32-100`；记录 Secure=false 限制，不建议部署时改源。 |
| `.trellis/spec/backend/middleware-guidelines.md` | 并入 Go 与鉴权规则 | `internal/app/router.go:87-109,134-200`、`internal/app/router_test.go:403`；不引入通用 CORS 假设。 |
| `.trellis/spec/backend/error-handling.md` | 并入 Go 与 HTTP 规则 | `internal/httputil/errors.go:105-189`、`internal/httputil/locale.go:137`；内因不出响应。 |
| `.trellis/spec/backend/logging-guidelines.md` | 并入 Go 与日志规则 | `internal/app/logger.go:68-199`、`internal/logsafe/core.go:44`、`internal/app/runtime.go:613`；以实际脱敏、队列与降级为准。 |
| `.trellis/spec/backend/quality-guidelines.md` | 并入 Go 与架构 | `Makefile:244-256`、`internal/task/store_fault_test.go`；旧 `TestCameraCreate` 选择器不代表测试覆盖。 |
| `.trellis/spec/backend/alertpush-guidelines.md` | 并入 Go 与数据 | `internal/alertpush/sender_webhook_test.go:420`、`internal/alertpush/signed_media.go:25-117`；保留签名、重定向约束，省略易老化协议选项表。 |
| `.trellis/spec/backend/openapi-and-asyncjob-guidelines.md` | 并入 Go | `internal/openapi/handler.go:72`、`internal/asyncjob/store.go:166-293`、`internal/asyncjob/service_test.go:379`；保留独立凭据与单向终态。 |
| `.trellis/spec/backend/database-guidelines.md` | 并入数据与 SQLite 规则 | `internal/database/store.go:141-181`、`internal/database/store_test.go:1607`；保留连接池与 PRAGMA，不将 GORM generics 绝对化。 |
| `.trellis/spec/backend/camera-guidelines.md` | 并入数据与媒体 | `internal/camera/store.go:159-215`、`internal/camera/lifecycle.go:83-121`、`internal/camera/audio_wire.go:11-28`；CAS 与密钥依赖、媒体通道分离。 |
| `.trellis/spec/backend/mediafile-guidelines.md` | 并入数据与媒体文件规则 | `internal/mediafile/resolver.go:46-172`、`internal/mediafile/resolver_test.go:227`；不能用字面路径前缀代替 rooted open。 |
| `.trellis/spec/backend/record-guidelines.md` | 并入 `domains/data-and-storage.md` 的记录、留存与「感知记录身份与属性格式」 | 同事务分页和证据重试见 `internal/record/store.go:48-115`、`internal/record/retention.go:211-340`；宿主身份、冷却升级/防倒退见 `internal/record/service.go:353-363,630`、`internal/record/cooldown_keys.go:18-135`；`@face` 持久化格式见 `internal/record/service_build.go:282-347`。原生快照命中与所有权归媒体规格，匹配与容量实现细节以源码为准，不把旧快照环大小当接口承诺。 |
| `.trellis/spec/backend/spatial-guidelines.md` | 并入数据 | `internal/spatial/service.go:228-330`、`internal/spatial/service_test.go:362,427,453`；区分未传与空集合。 |
| `.trellis/spec/backend/storage-guidelines.md` | 并入数据与媒体文件规则 | `internal/storage/service.go:132-199`、`internal/storage/gate_test.go:178,208`；保留准入和紧急清理，不承诺旧阈值。 |
| `.trellis/spec/backend/task-guidelines.md` | 并入数据 | `internal/task/store.go:99-157`、`internal/task/service.go:159-199`、`internal/task/store_test.go:366`；提交后通知及 crop-only 边界。 |

## 原生与算法

| 旧路径 | 处置 | 当前证据与取舍 |
| --- | --- | --- |
| `.trellis/spec/native/index.md` | 并入索引和两域 | `Makefile:69-83,124-147,215-225`；不继承历史 CTest 数量。 |
| `.trellis/spec/native/directory-structure.md` | 并入原生与 SDK | `native/CMakeLists.txt:131-160,335-404`；保留链接拓扑，不复制目录树。 |
| `.trellis/spec/native/cgo-contract.md` | 并入原生；离线提取转 SDK | `native/include/Zhulong/engine.h:265-325`、`internal/engine/engine.go:148-161`；ABI 版本取 C 宏。 |
| `.trellis/spec/native/ingestion-contract.md` | 并入原生 | `native/src/nodes/capture/rtsp_input.cpp:132-152,998-1042`、`native/tests/preview_normalizer_test.cpp:88-165`；保留输入与时钟契约，不移植固定用例数。 |
| `.trellis/spec/native/pipeline-guidelines.md` | 并入原生及生命周期规则 | `native/src/pipeline/engine.cpp:307-344`、`native/src/pipeline/frame_publisher.cpp:134-185`、`native/src/nodes/decode/decoder_factory.cpp:60-104`；补齐 Ascend 与快照非精确回退。 |
| `.trellis/spec/native/algo-plugin-contract.md` | 并入 SDK 及 IPC 规则 | `native/sdk/abi.json:2-26`、`native/src/algorithm/ipc/ipc_protocol.hpp:130-219`；区分内部 vtable 与进程协议。 |
| `.trellis/spec/native/hardware-backends.md` | 并入 SDK 与原生 | `native/CMakeLists.txt:182-235,353-404`、`native/tests/rga_backend_contract_test.cpp:479`；保留 DMA 同步和后端隔离，不继承旧动态插件命令。 |
| `.trellis/spec/native/host-portability.md` | 并入 SDK 与原生 | `native/src/algorithm/ipc/broker_resource_lock.cpp:63-74`、`native/src/algorithm/ipc/unix_socket.cpp:393-410`；移除旧 FacePluginLoader 路由和固定用例数量，不声称跨平台已验证。 |
| `.trellis/spec/backend/algorithm-guidelines.md` | 并入 SDK | `internal/algorithm/verifier.go:206-250,287-477`、`internal/algorithm/service.go:614-712`；旧 `plugin.so` 不是当前入口。 |
| `.trellis/spec/backend/gb28181-guidelines.md` | 并入原生与 Go | `internal/gb28181/doc.go:5-44`、`internal/app/app.go:246-289`、`internal/camera/scheduler_test.go:232-276`；控制/媒体隔离但 camera 域识别协议。 |

## 任务与项目接入

- `.trellis/tasks/11-12-stranger-cluster-trajectory/`、`.trellis/tasks/11-15-face-track-fusion-identity/`：有 `task.json`，状态均为 `planning`、优先级 `P2`；逐字保留 PRD 作为规划来源。PRD 内的历史勾选和旧文件位置不等于本次验收，也不转为已完成洛书任务。
- `.trellis/tasks/11-17-person-attributes/`、`.trellis/tasks/11-19-face-attributes/`：只含 PRD、设计，无任务元数据；保留原位，不建立活跃变更。其余 `.trellis/tasks/`、`.trellis/workspace/`、历史配置和脚本原位留存。
- 项目入口：`AGENTS.md`、`README.md`、`native/README.md` 的现行导航改指洛书；`CLAUDE.md` 仍通过 `@AGENTS.md` 跟随。源码注释中的历史测量引用、`.air.toml` 的旧目录排除、`.gitattributes` 的历史日志处理不调用 Trellis，不作为迁移目标。
- 本机项目接入：`.dsh/DSH.md` 说明 Trellis 为按需读取，无启动 hook；已识别 `.dsh/skills/trellis-*`、`.agents/skills/trellis-*`、`.agent/skills/trellis-*`、`.agent/workflows/{start,continue,finish-work}.md`、`.pi/prompts/trellis-*.md` 与 `.pi/agents/trellis-*.md`。仅停用验证过的项目文件，保留可逆副本。`.claude/settings.local.json` 是 MCP 配置，`.pi/settings.json` 和 `.pi/SYSTEM.md` 含其他平台配置，不触碰；未设置 `core.hooksPath`。用户级洛书/Trellis 安装不属迁移范围。

## 本机切换与回滚

- 本机将上述 30 个 Trellis 技能/命令/Agent 入口和旧 DSH 指南（合计 31 项）按原路径移到 `.trellis/.runtime/luoshu-cutover-2026-11-22/`；该目录是 Git 忽略的本机备份，`manifest.txt` 列出原相对路径。新 `.dsh/DSH.md` 只引导到洛书。已安装的用户级 DSH 洛书技能无需重复安装；其他机器可直接使用洛书 CLI。
- 回滚前先备份当前工作区及本机新指南，再将清单中的文件或目录按相对路径恢复，遇到同名内容先比较，不覆盖后来用户改动；若要恢复仓库入口，单独还原本变更对 `AGENTS.md`、`README.md`、`native/README.md` 的修改。不需重建 `.trellis/` 历史。

## 洛书目录布局

- 13 条迁移与接入规则按算法、数据、Go 宿主、原生和 Web 分层。当前 CLI 按相对路径生成 ID，三个变更同步改用带目录前缀的 ID；规则文件名、触发 glob、优先级与约束内容不因分层而变，证据链接按新目录深度修正。
- 另经用户确认，将新增的 [Go 代码风格规则](../../rules/go-host/code-style.md) 纳入本次迁移交付，总计 14 条规则。它不来自上述 40 份 Trellis 文档，也不改变逐篇处置统计；保留其现有内容，仅按 `internal/**/*.go` 加载，不设为常驻或显式挂载，不据此重写存量代码。
- 六份规格从总索引经 `architecture/index.md`、`domains/index.md` 进入。三个变更挂载指向新位置，不更改已批准的规格范围或静态验收契约。

## 验证边界

- `make check` 在 bootstrap 实测退出 2：Vitest 无法加载 `web/src/test-utils/setup.ts`（`Failed to fetch dynamically imported module`），以及 `Cannot connect to the iframe`。完整链路未走完；本次只迁移工作流，不处理浏览器测试问题，也不以部分检查替代，因此保留 `operations.verify.gates: []`。
