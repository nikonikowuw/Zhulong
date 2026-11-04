# PRD: 双轨联调（D · 门禁与实测）

> 父任务：`.trellis/tasks/11-03-dual-track-preview/`（权威来源）。依赖 A / B / C 全部合入。

## Goal

给 AC-1~AC-6 补齐**可复现的证据**，并把“不收听音频 = 今日行为”变成可以随时重跑的门禁，而不是口头承诺。

## Scope（父任务 Phase 5）

1. `make check` + `make cross-board` 全绿；
2. 新增内嵌 SPA 新鲜度门禁（`cross` / `cross-board` 当前不重建前端，陈旧 `dist` 不会报错）；
3. 同步实测：白闪 + 1kHz beep，240fps 采样，稳态中位数 ≤ 80ms、重同步后 500ms 内回收、10 分钟不发散；
4. RK3568 板端：16 路 / 30 分钟 / 其中 1~4 路处于音频收听状态；CPU、RSS、订阅断链、音频通道接管与溢出计数、`GOPTruncations` 与“完全不收听”的基线对照；
5. 降级与回滚演练：无音频相机、`audio-latm`、`audio-broken`、旧前端（缓存 bundle）、新前端遇旧后端、同会话接管与双会话并行；
6. 文档：`config.toml` 注释、README 预览能力、spec（camera-guidelines §5 / ingestion-contract §3.5 / cgo-contract / frontend 播放器时钟契约）、`internal/apidocs`。

## Out of Scope

- 新功能与代码重构；本子任务只允许为通过门禁做最小修复，且修复要回归到对应子任务的设计契约。

## Acceptance Criteria（父任务 AC-5 / AC-6）

- [ ] `make check` 全绿（frontend lint/test/build + native-test + api-docs + go-check），无数据竞争；
- [ ] `make cross-board` 通过；陈旧 `dist` 会被门禁拦下；
- [ ] 板端 30 分钟无断链、无 RSS 单调增长，CPU 有量化对照；
- [ ] 同步实测三条曲线入档（`research/sync-measurement.md`）；
- [ ] 回滚演练记录入档（`research/rollback-drill.md`）；
- [ ] 首次建流日志可见 `hasAudio / codec / sampleRate / 降级原因`；音频通道的建立/接管/溢出/关闭原因可查询；
- [ ] spec 与实现一致，配置项注释含带宽代价与生效时机。

## Dependencies

A + B + C。这是父任务唯一需要真实硬件的部分，允许在前三者归档后单独长时间开着。
