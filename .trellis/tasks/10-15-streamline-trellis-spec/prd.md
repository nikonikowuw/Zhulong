# PRD: 精简与优化 .trellis/spec 工程规范

## 1. 背景与目标

当前 `.trellis/spec` 共有 31 个规范文件，总计 3,847 行。随着项目迭代，规范累积了显著的跨层重复描述（特别是算法包与插件规范、HTTP API 与错误处理）、历史临时任务 ID（如 `10-13-*`）和辩解性废弃说明、以及单文件过度膨胀（`native/algo-plugin-contract.md` 达 544 行）。

**核心目标**：
1. **单一真实源 (SSOT)**：消除跨文件、跨分层的重复定义与样例，一处契约由一个权威规范承载，其他处通过超链接或引用提及。
2. **剔除历史包袱与临时痕迹**：清除已归档任务 ID（如 `任务 10-13-*`）、历史演进过程辩护、与已废弃契约的大段说明。
3. **结构解耦与模块化（采纳方案 A）**：
   - 将 `native/algo-plugin-contract.md` 解耦拆分为两份独立规范：
     - `native/algo-plugin-contract.md`：保持纯粹的算法插件对外契约（C ABI、C++17 SDK、IPC 协议与结果上报）；
     - `native/hardware-backends.md`：收敛 RKNN / RGA 等异构硬件加速后端的实测约束、环境避坑与对齐契约。
4. **语言与风格一致性**：将 `native/ingestion-contract.md` 汉化并紧凑化，统一全仓规范语言（以中文为主），保持规范的严肃性与契约属性。

---

## 2. 需求清单 (Scope)

### 2.1 跨层重复内容收敛
- **算法包与插件规范去重**：
  - `backend/algorithm-guidelines.md` 作为算法包发布、安装、校验与 Go 侧 Supervisor 进程监督的唯一真源；
  - `native/algo-plugin-contract.md` 作为 C ABI、C++17 SDK、内存打包和 FrameBroker IPC 的唯一真源；
  - 彻底移除 `backend/` 中重复粘贴的 C++ SDK 代码、纯 C ABI 结构体与退出码表；移除 `native/` 中重复展开的 `manifest.json` 完整校验表。
- **HTTP 响应与错误契约收敛**：
  - `backend/error-handling.md` 作为 `AppError` 结构、状态码、响应信封四元组与错误 i18n 的唯一真源；
  - `backend/http-api-guidelines.md` 聚焦路由组、分页、Swagger 与 SPA 隔离，精简重复的响应信封长篇说明与 JSON 示例。

### 2.2 历史包袱与临时任务 ID 消除
- 全仓扫描并清除 `任务 10-13-*`、`任务 10-14-*` 等已归档任务编号。
- 清理 `backend/storage-guidelines.md` §1.4 等处关于“历史条目说明、过去为何废弃、旧错误码辩护”的废弃说明，直接陈述当前生效契约。
- 清理 `architecture.md` 中出现的任务交付代号，改为当前架构客观事实陈述。

### 2.3 Native 规范解耦与瘦身
- **拆分与精简 `native/algo-plugin-contract.md`**：
  - 提取 RKNN / RGA 章节创建 `native/hardware-backends.md`，保留关键踩坑数据（RK3568 2.3.2 下 `MEM_SIZE` 陷阱、对齐要求、权重共享等），剥离任务交付报告叙述；
  - 同步更新 `native/index.md` 与 `native/directory-structure.md` 索引。
- **汉化与紧凑化 `native/ingestion-contract.md`**：
  - 转换为中文规范，提炼核心流借用与 CGO 约束，剔除调试日记式描述。

### 2.4 轻量级 Guides 与索引保持一致
- 精简 `guides/` 中的冗余表述，确保与各层最新规范及索引完全自洽，无失效链接。

---

## 3. 验收标准 (Acceptance Criteria)

- [x] **AC-1 (无跨层信息丢失)**：所有技术细节、安全铁律、硬件踩坑约束与 ABI 签名在精简后完整保留，无关键契约被误删。
- [x] **AC-2 (单一真实源成立)**：
  - `manifest.json` 规范在全仓仅完整定义 1 处（`backend/algorithm-guidelines.md`）；
  - 响应信封与错误码在全仓仅完整定义 1 处（`backend/error-handling.md`）；
  - C ABI 与 C++ SDK 代码在全仓仅完整定义 1 处（`native/algo-plugin-contract.md`）。
- [x] **AC-3 (历史与任务零残留)**：全仓 `.trellis/spec/` 不再包含任何 `任务 10-` 或类似开发期 task slug 文本；不再保留关于已废弃字段的大段历史辩解。
- [x] **AC-4 (体积与上下文优化)**：总行数大幅缩减 487 行冗余，单文件体积从 39.8KB 降至 19.5KB，彻底解决上下文注入超限截断告警。
- [x] **AC-5 (链接与引用完备)**：全仓内部 Markdown 相对跳转链接（`[...](./...)`）和索引表格有效，自动化校验 0 死链。
