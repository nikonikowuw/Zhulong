# 技术设计：项目全栈骨架

## 目标与边界

建立在开发机无摄像机、无 NPU 条件下可构建、启动和测试的纵向工程骨架。运行时由 Go 单宿主托管 Vite 静态资源，并通过 CGO 调用同仓 C++ 静态库。本阶段验证工程构建、服务生命周期和 ABI，不提供视频处理能力。

本轮不接入 RTSP/ONVIF、编解码、录像、算法推理或厂商 SDK，不声明目标板卡或 Linux BSP 兼容性。

## 系统结构

```text
web/ React + TypeScript + Vite
  └─ build -> internal/webui/dist -> Go embed

cmd/Zhulong
  └─ internal/app (Fx 装配与有序生命周期)
       ├─ Gin API / SPA / Swagger
       ├─ internal/database (GORM + SQLite + embed SQL migrations)
       ├─ internal/httputil (统一响应与错误契约)
       └─ internal/engine (CGO wrapper)
            └─ native/include/Zhulong/engine.h
                 └─ native C++17 静态库 (CMake)
```

Go 代码遵循已有业务/模块边界。Fx 只出现在 `internal/app`；构造函数不打开数据库、不启动服务、不分配硬件资源，也不启动长期 goroutine。

## 运行时契约

### 启停顺序

由单一应用生命周期协调器显式管理启动、部分失败回滚和停止顺序：

1. 打开 SQLite，设置连接参数并执行嵌入式版本化 SQL 迁移。迁移失败时启动失败，HTTP 尚未绑定。
2. 创建并启动无硬件依赖的 C++ 引擎生命周期 stub。
3. 绑定 HTTP listener 并启动 Gin 服务。

后续步骤失败时按逆序释放已取得资源。正常关闭时依次优雅停止 HTTP、停止并销毁 native engine、关闭 SQLite、Sync Zap。长期服务不保留 Fx `OnStart` 的短生命周期 context；服务关闭使用 `OnStop` context。

迁移采用 `golang-migrate/migrate/v4` 的 `iofs` source 读取 `//go:embed` SQL 文件（`<version>_<name>.up.sql` / `.down.sql`），并使用 SQLite database/sql adapter 执行；SQLite 业务访问采用 GORM SQLite driver。禁止使用 GORM `AutoMigrate`。迁移和 SQLite 依赖版本在实现时写入 `go.mod`/`go.sum` 并锁定。

### HTTP 与内嵌前端

- `GET /api/v1/health` 在应用就绪后返回标准成功信封。
- `/swagger/*any` 提供生成的 Swagger 2.0 文档。
- `/api/*` 未匹配时返回 HTTP 404 JSON 错误，绝不回退到 SPA HTML。
- `/` 和无扩展名浏览器路由返回内嵌 `index.html`；不存在的静态资源返回 404。
- 所有 JSON 响应必含 `code`、`message`、`data`。错误响应的 `data` 为 JSON `null`。仅 HTTP 422 字段校验错误可包含 `details`。
- 单项字段详情为 `{ "field": "<request JSON path>", "code": "<stable code>", "message": "<localized text>" }`。字段文案按 `Accept-Language` 本地化；不得回显原始输入或内部错误。非 422 错误不返回 `details`。

前端提供最小系统状态 shell，采用 React、严格 TypeScript、Vite、React Router、TanStack Query、Tailwind 语义 token 和 `react-i18next`。基础 shell 覆盖英文、简体中文、繁体中文及系统浅/深色偏好，不实现任何产品业务工作流或表单提交。

### Native ABI 与内存所有权

- CMake 构建不依赖厂商 SDK 的 C++17 静态库。
- `native/include/Zhulong/engine.h` 是 Go 唯一可包含的 native 头文件，暴露纯 C 不透明句柄、固定宽度状态码及 create/start/stop/destroy 接口，并使用 `extern "C"` 防止 C++ ABI 外泄。
- 所有导出函数均捕获 C++ 异常并转为状态码。
- 引擎 stub 使用确定的状态转换，不创建 worker thread、不保留帧/缓冲区。生命周期可在无设备环境下测试。
- 不透明句柄由 C++ 分配和销毁；Go wrapper 只保存 C 所有的句柄，并串行化使用与 Close。骨架不向 C 传递 Go 指针，不注册异步回调。

## 构建与验证

根目录 Makefile 是唯一受支持的干净检出构建入口，顺序为前端静态构建、CMake native 静态库构建、Go 主程序编译。这样 CGO 链接不依赖隐式人工步骤。生成文件落在忽略的 build/dist 目录；Go embed 包保留一个跟踪的非空 sentinel，确保首次前端构建前 Go 包仍可测试。单二进制 smoke test 验证健康 API 和内嵌 SPA。

本机基线为 macOS arm64、Go 1.27.1、Node 24.15.0/npm 11.12.1、CMake 4.4.0、Apple Clang 17.0.0。CMake/CGO 提供本机 host build 及通用 Linux 编译器/运行库配置，但在实际目标工具链验证前不承诺交叉编译或硬件支持。

## 关键决策与取舍

| 决策 | 理由 | 取舍 |
| --- | --- | --- |
| CMake 静态库 + 根目录 Makefile | 原生层可独立测试，CGO 链接顺序可复现 | 干净检出不以直接 `go build` 为支持入口，应使用文档化根级命令 |
| GORM SQLite + golang-migrate/`iofs` | 版本化 SQL 与 embed FS 匹配，避免 schema 自动推断 | SQLite 与 native 都需要启用 CGO 和 C 工具链 |
| 三个必需响应字段 + 可选校验 `details` | 落实用户确认的稳定信封并支持前端字段反馈 | 客户端 schema 必须容许可选字段；字段细节只能用于 422 校验错误 |
| 最小本地化状态 shell | 同时验证前端构建、主题/i18n 基础和 Go embed | 不交付相机或设备管理能力 |
| 无厂商 runtime 的 native stub | 生命周期测试可以在开发机执行 | 板端驱动、推理和媒体路径尚未验证 |

## 风险与控制

- **CGO/C++ 链接差异：** Makefile 先构建静态库；按宿主选择 C++ runtime 链接参数；先验证本机路径，不把它等同于交叉编译验证。
- **内嵌资源缺失：** 根级 build 固定先运行 Vite；用 sentinel 保证 Go package compile，并对构建后的二进制做 smoke test。
- **部分启动泄漏：** lifecycle coordinator 负责回滚；测试迁移失败和 HTTP bind 失败时没有服务残留。
- **合同漂移：** 产品 PRD、backend API/error specs、Swagger schema 和测试共用响应定义；字段详情不含敏感值。
- **现有未提交改动：** 保留 `AGENTS.md`、`docs/`、`go.mod` 和 `.trellis/config.yaml` 现有内容，仅对用户明确要求的 PRD/API 规范和骨架所需文件做增量修改。
