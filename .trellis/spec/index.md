# Zhulong 项目工程规范总览

> Zhulong（基于深度学习的轻量化网络视频录像机 NVR）全链路架构契约、编码守则与质量标准。

---

## 1. 规范导航地图

| 分层 | 规范入口 | 核心覆盖范围 | 查阅时机 |
| --- | --- | --- | --- |
| **系统架构** | [architecture.md](./architecture.md) | 全局拓扑、单主程序交付模型、跨层防爆门、待决选型 | 跨层设计与全局规划 |
| **跨语言命名** | [naming-guidelines.md](./naming-guidelines.md) | 文件、CLI 命令/参数、函数/方法、变量及领域术语 | 新增、重命名或评审标识符时 |
| **Go 后端** | [backend/index.md](./backend/index.md) | 业务分包、Gin 路由、GORM+SQLite、Fx 生命周期、统一错误/日志 | 编写或评审 Go 代码 |
| **原生 C++/CGO** | [native/index.md](./native/index.md) | C ABI 门面、CGO 指针/内存契约、Node 流水线、有界队列、安全停机 | 音视频、推理及 CGO 开发 |
| **React 前端** | [frontend/index.md](./frontend/index.md) | shadcn-admin 控制台、12 条编码铁律、TanStack Router/Query/Table、OKLCH 主题与三语 i18n | 编写或评审前端组件 |
| **思考指南** | [guides/index.md](./guides/index.md) | 跨层数据流排查清单、代码复用与单一真实源原则 | 跨多层或抽公共能力时 |

---

## 2. 全局核心铁律

1. **方案 A 单主程序交付**：前端构建产物 `//go:embed` 内嵌至 Go 二进制；C++ 引擎内联链接；允许依赖宿主 Linux 动态库（glibc/NPU 驱动）；SQLite 与录像外置。
2. **Go 业务能力分包**：按业务域划分（`internal/camera`, `internal/recording`, `internal/inference`），严禁横向技术分层与循环依赖。
3. **Fx 显式生命周期编排**：构造函数保持纯粹，由 Fx 统一装配；**启动时必须先自动执行版本化 SQLite 迁移**，成功后方可放行 HTTP 流量与启动 Pipeline。
4. **统一响应信封与后端 i18n**：所有 JSON 响应必含 `{ code, message, data }`，HTTP 422 字段校验错误可附加 `details`；错误时 `data: null`。后端按 `Accept-Language` 翻译顶层与字段级消息；底层细节在服务端 Zap 日志中严格脱敏。
5. **C ABI 绝对隔离**：CGO 仅通过 `include/Zhulong/engine.h` 交互（不透明句柄）；C++ 异常绝对禁止穿越 C ABI；内存遵循“谁分配谁释放”。
6. **前端 12 条铁律**：纯函数组件与 Hook、严禁 `any`、单一职责按需拆分、服务端状态独占归 TanStack Query、表格 URL 状态同步、完整支持英/简中/繁中三语。
7. **必要注释**：注释应说明代码本身无法清楚表达的意图、关键约束或非显而易见行为（如所有权、生命周期、并发与错误语义），不得逐行复述实现；实现变化时同步更新或删除过期注释。

---

## 3. 仓库现状与规划约定区别

- **仓库客观事实**：`go.mod` 声明模块 `github.com/nikonikowuw/Zhulong`。当前为全新绿地仓库，尚无业务代码或迁移文件。
- **本规范定位**：所有目录树与代码示例均为**规划契约**，用于指导渐进式实现，切勿在开发具体功能前预先创建空目录或占位文件。
