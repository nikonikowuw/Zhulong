<!-- TRELLIS:START -->
# Trellis 指引

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
