# Implement Plan: 事件录像片段

分阶段推进，每阶段可以单独验证、单独提交。

## Phase 0 — 准备
- [ ] 通过 `trellis-before-dev` 读 spec：`native/cgo-contract.md`、`native/pipeline-guidelines.md`、`native/ingestion-contract.md`、`backend/{record,storage,database,http-api,dependency-injection,task}-guidelines.md`、`frontend/index.md`、`architecture.md`。

## Phase 1 — FFmpeg 与 Native 录像节点
1. [ ] `native/scripts/build.py`：FLAGS 加 `--enable-muxer=mp4 --enable-protocol=file --enable-demuxer=mov`；`make native-deps`。
2. [ ] `native/src/nodes/record/fmp4_writer.{hpp,cpp}`：写 `.part`、rename、Annex B 参数集 → extradata。
3. [ ] `native/src/pipeline/clip_recorder.{hpp,cpp}`：GOP 环形缓冲、片段状态机、写线程、有界队列、截断。
4. [ ] `Stream` 持有 recorder，在 `dispatch` 中接入（含提前返回条件）、`gop_id` 跳变处理、析构收尾。
5. [ ] engine 级完成事件队列。
6. [ ] `engine.h` v7 ABI + `native/src/abi` 实现（`boundary`）。
7. [ ] Native 单测：缓冲淘汰（时长与字节上限）、起点对齐关键帧、合并延长、120s 切分、写队列溢出 → truncated、gop 跳变、H.264/HEVC 写出后用 mov demuxer 读回（首帧关键帧、时长、帧数）。
- 验证：`make native-build && make native-test`

## Phase 2 — Go 引擎绑定与 camera
8. [ ] `internal/engine/clip.go` + 单测（ABI 版本、错误映射）。
9. [ ] `camera.MediaStream` 扩展、`EngineMediaAdapter`、`StreamHub.{SetClipPolicyProvider,RefreshRecorders,TriggerClip}`，acquire 后应用配置；更新测试桩。
- 验证：`python3 native/scripts/build.py go test -race ./internal/engine ./internal/camera`

## Phase 3 — 数据与领域
10. [ ] 迁移 `000037_create_event_clips.{up,down}.sql`（对照 alarms 现有 FK 写法）。
11. [ ] `internal/task`：`ClipConfig` 解析与校验、requests/responses、`ClipPolicyCache`、locale 错误码。
12. [ ] `internal/recording`：Store、Service（Trigger / 轮询 / 启动修正 / 清理 / Stop）、单测（fake hub + 临时目录 + sqlite）。
13. [ ] `internal/record`：`ClipTrigger` 窄接口、在三个入库点触发、识别过滤、model 字段、响应 `ClipInfo` 装配；单测覆盖 AC2/AC3/AC5。
14. [ ] storage 回归测试：`evt-{id}.mp4` + `.lock` 两阶段都不删；无锁的过期片段被删（AC7）。
15. [ ] app 接线：Fx module、`lifecycleRuntime` Start/Stop 顺序、listener 接线。
- 验证：`make go-check`

## Phase 4 — 前端
16. [ ] `records`：schema、`record-clip-panel.tsx`、列表图标、locales；浏览器测试（H.264 小样本播放与定位、不支持时的提示）。
17. [ ] `tasks`：录像配置区、payload / schema / draft、locales、测试。
- 验证：`cd web && pnpm test`、`make frontend-build`

## Phase 5 — 收尾
18. [ ] 更新 spec：`architecture.md`（连续录像那一行 + 事件片段）、`record-guidelines.md`、`storage-guidelines.md`（文件名禁止 `_`）、`native/pipeline-guidelines.md`（Decode→Storage 改为不反压、截断）、`native/cgo-contract.md`（v7）、`backend/directory-structure.md`。
19. [ ] `make check`；`make cross-board`（只编译）。
20. [ ] （有硬件时）RK3568 实测：4 路 1080p/4K 的内存与 CPU，校准 32 MiB / 16 MiB / 120s 默认值。

## 风险文件 / 回滚点
- `native/src/pipeline/stream.cpp` 的 `dispatch`：热路径，改动要最小化；recorder 为空时只多一次指针判断。
- `native/scripts/build.py`：改 FLAGS 会让所有 FFmpeg 缓存失效（本机与交叉都要重建）。
- `internal/record/service.go`：只在入库成功后追加调用，不改动现有的分发和抓拍顺序。
- 回滚：任务开关默认关闭；迁移有 down。
