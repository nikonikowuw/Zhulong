# AGENTS.md

## 项目速览

Zhulong 是本地优先的边缘运维宿主：单个 Go 二进制内嵌 React SPA、执行版本化 SQLite 迁移，
并经一套 C ABI 静态链接 C++17 原生引擎。交付模型（「方案 A」）是一个可执行文件，运行时零 Node.js 依赖。

- 前置依赖与功能边界：`README.md`
- 开发流程、任务与规范导航：`.trellis/workflow.md`、`.trellis/spec/index.md`

## 命令

`Makefile` 是命令的唯一来源：

- **绝不**对（间接）依赖 `internal/engine` 的包直接跑裸 `go build`/`go test`/`go vet`。
  native wrapper 提供显式的静态 FFmpeg 归档和一个*内容寻址*的 Engine 归档；Go 不对第三方归档做哈希，
  固定的 `-L` 路径会静默复用陈旧库。Make 与 Air 已走 wrapper。
- 改完 C++ 必须 `make native-build` —— Air 只监听 Go/TOML。
- 跑单个测试：engine 相关走 wrapper，其余可以直接 `go`：

  ```bash
  python3 native/scripts/build.py go test -race ./internal/camera -run TestCameraCreate
  go test -race ./internal/httputil -run TestSuccess
  cd web && pnpm test -- src/features/cameras/api/cameras-api.test.ts
  ```

- `make go-check` 刻意限定在 `./cmd/... ./internal/...`，以免 `./...` 递归进 `web/node_modules` 里的 Go 示例文件。
- 交叉编译需要 `CROSS_PROFILE` + `CROSS_OUTPUT`，产物必须在 `build/targets/<name>/` 下；
  交叉**只允许编译，不允许执行测试**。
- 收工前过 `make check`（前端 lint/type-check/test + native-test + api-docs + go-check）。

## 架构边界速查

只有下面这些是「写错就出事」的；完整拓扑与跨层契约看 `.trellis/spec/architecture.md` 与各层 spec。

**Go 宿主（`internal/app`，Uber Fx）**

- 每个领域暴露一个 `New*Components(...)`。构造函数必须纯粹：不开文件、不分配原生状态、不绑 socket ——
  那些放生命周期钩子。
- 路由走 Fx value group（`public_routes` / `protected_routes` / `upload_routes`），不是中央列表；
  新增端点实现 `RegisterRoutes(*gin.RouterGroup)` 并挂进对应 group。
- 健康检查的 `ready` 只代表数据库**与**引擎就绪，与摄像头无关。

**HTTP 层**

- 响应一律用 `internal/httputil` 的信封 `{code,message,data,details?}`，成功恒为 `code: "OK"`；
  handler 里**绝不**写裸 `c.JSON`。
- 错误返回 `apperr.Error`（`Kind` + 稳定机器码 + 内部 cause）。`httputil.WriteError` 负责映射 HTTP 状态码、
  按 `Accept-Language` 选本地化文案、且只在服务端记录 cause；code 与文案在 `internal/httputil/locale.go`。
- 未匹配的 `/api/*` 返回 JSON 错误信封，其余未匹配 GET/HEAD 回退到内嵌 SPA。
- `internal/httpmiddleware` 必须与业务无关。

**持久化**

- **不用 `AutoMigrate`。** Schema 变更 = 在 `internal/database/migrations/` 新增一对编号 SQL
  （`00000N_name.up.sql` / `.down.sql`）；已应用的迁移绝不修改，只追加。
- 领域 store 依赖 `database.DBProvider` 接口，不是 `*gorm.DB`。

**原生引擎边界**

- `native/include/Zhulong/engine.h` 是唯一公开头文件；`internal/engine` 是唯一可 `import "C"` 的包。
- 绝不向 C 传 Go 指针（回调 token 是 `cgo.Handle` 整数）；C++ 异常绝不穿越 ABI
  （每个导出函数隔离异常并返回状态码）。
- packet 视图**仅在同步回调期间**有效，要保留就拷贝。
- 所有权显式：`Engine.Start` → `engine_create`，`Engine.Close` → `engine_destroy`；不靠 GC 释放 C 内存。
- `make native-deps` 是唯一联网的构建步骤（固定版 FFmpeg + SHA-256 校验）；其余离线且做缓存身份校验，
  构建期不下载、不动态链接 FFmpeg。

**前端（`web/`）**

- 特性切片：`src/features/<feature>/{api,hooks,components,data,locales}`，从 `index.tsx` 出页面入口；
  `src/routes/**` 只做路径声明、鉴权守卫、参数校验。跨特性只走对方的 `index.tsx`。
- `src/routeTree.gen.ts` 是生成物，不手改；`src/lib/api-client.ts` 注入 `Accept-Language` 并拆 Go 信封；
  不可信输入用 Zod 解析而非断言类型。
- `src/components/ui/**` 是改造过的 shadcn/ui fork：未合并本地改动前**不要** `shadcn add --overwrite`。
- Vite 把 `/api` 代理到 `127.0.0.1:8000`（与仓库内 `config.toml` 一致），而编译内置默认是 `8080`
  （`app.DefaultConfig`）—— 改端口要同步这两处。
- 发布前确认 `internal/webui/dist` 的内嵌副本是最新的。

**配置**：`config.toml` 从 CWD 读取，viper 严格模式（未知键即中止启动）；数据目录存放 `zhulong.db`、
摄像头凭据密钥（`camera.key`）与算法包；`[algorithm].target` 必须与某包的 `manifest.json` 中
`platform.target` 一致。

## 硬性约定

- 测试与源码同目录（`*_test.go`、`*.test.ts(x)`），尽量表驱动；Go 的 `-race` 强制，数据竞争是阻塞级 bug。
- 前端测试跑在**真实无头浏览器**里，不是 JSDOM —— 不要写只在 JSDOM 下成立的假设。
- 业务路径无 `panic`；不用 `_ = fn()` 吞错；不把 `context.Context` 存进结构体字段；
  无全局可变业务状态（通过构造函数注入）。
- 动手前读对应 spec：`.trellis/spec/backend/index.md` 与 `frontend/index.md` 各带
  Pre-Development Checklist，ABI 看 `.trellis/spec/native/*`，跨层边界看 `.trellis/spec/architecture.md`。

<!-- TRELLIS:START -->
## Trellis 指引

本项目使用 Trellis 管理。项目工作所需的信息位于 `.trellis/`：

- `.trellis/workflow.md`：开发阶段、任务创建要求和技能路由
- `.trellis/spec/`：按软件包和层级组织的开发规范（编写代码前请先阅读）
- `.trellis/workspace/`：开发者日志和会话记录
- `.trellis/tasks/`：进行中和已归档的任务

如果当前平台提供 Trellis 命令（例如 `/trellis:finish-work`、`/trellis:continue`），优先使用这些命令，而不是手动操作。

如果使用 Codex 或其他支持子代理的工具，项目中还可能提供专用辅助配置：

- `.agents/skills/`：可复用的 Trellis 技能
- `.codex/agents/`：可选的自定义子代理

此区域由 Trellis 管理；后续运行 `trellis update` 时可能会覆盖其中的修改。区域外的内容会保留。

<!-- TRELLIS:END -->

## Pi 子代理约定

在 Pi Agent 中委派统一使用 `npm:pi-subagents` 提供的 `subagent` 工具；不要调用 Trellis 自带的 `trellis_subagent`。本项目当前的 `.pi/extensions/trellis/index.ts` 只注册会话/上下文事件，没有注册原生 `trellis_subagent` 工具。单个子代理直接使用 `{ agent, task }`，不要设置 `workflow: true`。只有需要脚本编排时才使用 `workflow: true`，并在同一条回复中提供唯一一个 `js workflow` 代码块。委派 Trellis 任务时，提示首行写 `Active task: <task path>`，让子代理加载对应任务上下文。
