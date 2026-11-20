# 事件录像片段（Event Clip）

## Goal

告警或识别事件发生时，自动保存该事件**前后各一段**的主码流视频（单个 MP4 文件），运维可以在告警/识别详情里直接回放、下载，作为比单张抓拍更完整的证据。

## Background / Confirmed Facts

- 事件来源：`internal/record` 写入 `alarms` / `recognitions` / `captures`，并按 cooldown 去重。
- AI 分析流固定拉 **main** 角色，以 `ConsumerAI` 挂在物理流上（`internal/camera/stream_hub.go`）。
- `engine.h` 已预留 `Zhulong_CONSUMER_RECORDING = 2`；`Zhulong_packet_view` 已携带 `pts_us/dts_us/key_frame/gop_id/track_type`。
- 静态 FFmpeg 目前只启用了 rtsp demuxer、h264/hevc parser/decoder 和 mjpeg encoder，**没有 mp4 muxer，也没有 file 协议**（`native/scripts/build.py`）。
- `recordings/` 目录约定、`RecordingsRetentionDays`、mediafile 白名单与 `video/mp4` MIME 都已存在；spec 写明 recorder 数据面尚未实现（`.trellis/spec/architecture.md` 「连续录像」一行）。
- 任务配置：`tasks`（每相机一条）+ `task_algorithms`（`config_json` / `region_config_json` / `schedule_json`）。

## Requirements

### R1 触发
- R1.1 只有**告警**和**识别**事件触发录像；目标抓拍（captures）**不**触发。
- R1.2 识别事件提供触发过滤：`all`（默认）/ `stranger`（仅 unmatched）/ `known`（仅 matched）。
- R1.3 事件经 cooldown 去重后才触发（被抑制的事件不触发）。

### R2 配置（按 task_algorithm）
- R2.1 每个 task_algorithm 单独配置：录像开关（默认关）、前段秒数（默认 5，范围 1–15）、后段秒数（默认 10，范围 1–60）、识别过滤（R1.2）。
- R2.2 同一相机的环形缓冲长度取该相机所有开启录像的任务前段的最大值；另设每路字节硬上限（默认 32 MiB），超过上限时从最老的 GOP 开始淘汰。
- R2.3 没有任何开启录像的任务时，该相机不分配缓冲。

### R3 录制
- R3.1 录 **main** 码流；从 AI 已拉起的同一路物理流在解码前旁路取包，不额外建立 RTSP 连接。
- R3.2 原样 remux，不转码；首期只录视频（H.264 / H.265），ABI 与表结构预留音频。
- R3.3 片段起点 = 事件 PTS − 前段时长，再向前对齐到最近的关键帧；缓冲不足时从缓冲内最早的关键帧开始（片段前段变短，不算错误）。
- R3.4 同一相机合并延长：片段还在录后段时再来事件，就把结束时间顺延到「新事件 + 该任务后段」，单个片段总长 ≤ 120s；超过则结束当前片段，新事件开新片段。
- R3.5 每个关联事件记录 `clip_offset_ms`（事件在片段内的偏移）。
- R3.6 容器为 fMP4（`frag_keyframe+empty_moov`），文件写一半时已写入的分片也能播放。

### R4 可靠性与资源
- R4.1 录像写盘**绝不**反压拉流、解码、推理：每路一个写线程 + 有界队列；队列溢出时在下一个关键帧处截断，片段标为 `truncated`。
- R4.2 storage 写门关闭时不发起录像：事件照常入库，`clip_id` 为空。
- R4.3 录像失败绝不影响事件入库与事件推送。
- R4.4 启动时修正残留的 `status=recording` 行：文件存在且非空 → `completed`（标记 partial）；否则 → `failed`。
- R4.5 停机时 flush 正在录的片段（写到当前为止），并把状态写对。

### R5 数据与存储
- R5.1 新表 `event_clips`：id、camera_id、相对路径、codec、started_at、ended_at、duration_ms、size_bytes、status（`recording` / `completed` / `truncated` / `failed`）、partial、created_at。
- R5.2 `alarms` / `recognitions` 新增可空的 `clip_id`（FK → event_clips，`ON DELETE SET NULL`）和 `clip_offset_ms`。
- R5.3 路径：`recordings/{camera_id}/{YYYY-MM-DD}/evt-{clip_id}.mp4`，写入过程中用 `.part` 后缀。⚠️ 文件名不能含 `_`：storage 的 `mediaPrefix` 以第一个 `_` 分组（`internal/storage/cleaner.go:727-734`），`evt_` 会让同一目录下所有片段并成一个清理单元，一把 `.lock` 就锁住整天。
- R5.4 只被识别引用的片段参与 `RecordingsRetentionDays` TTL 和水位淘汰；**被任一告警引用的片段加 `.lock`**，与告警抓拍图的铁律一致，严禁自动清理。
- R5.5 片段文件被清理后，`event_clips` 行随之删除（事件行的 `clip_id` 由 FK 置空）。

### R6 API 与前端
- R6.1 告警/识别的列表与详情响应带 `clip` 投影（`*Info`）：id、status、durationMs、offsetMs、mediaPath（可交给 `getMediaUrl`）、codec。
- R6.2 告警详情、识别详情内嵌播放器：codec 受支持时用 `<video>` 播放并自动跳到 `offsetMs`；不受支持（如浏览器不支持 HEVC）时提示「当前浏览器不支持该编码，请下载播放」；任何情况都可下载。
- R6.3 状态为 `recording` 时显示「录制中」，可手动刷新；列表行显示「有录像」图标。
- R6.4 任务算法配置表单新增录像区：开关、前段秒数、后段秒数、识别过滤（只对识别类算法显示）。
- R6.5 中英文文案齐全（i18n）。

## Acceptance Criteria

- [ ] AC1（R1/R3）开启录像的告警算法产生一条告警后，告警行 `clip_id` 非空，约「前段+后段」秒后文件写完，`status=completed`，用 ffprobe 看时长 ≈ 前段+后段（误差 ≤ 1 个 GOP），首帧为关键帧。
- [ ] AC2（R1.1）captures 永远不触发片段；识别过滤 `stranger` / `known` 生效（有单测）。
- [ ] AC3（R3.4）同一相机 3s 内连续触发 3 个事件，只生成 1 个文件，3 个事件引用同一 `clip_id` 且 `clip_offset_ms` 单调递增；超过 120s 时切出第二个片段（native 单测 + Go 单测）。
- [ ] AC4（R4.1）模拟写线程卡住时，AI 帧投递与预览不受影响，片段标 `truncated`（native 单测）。
- [ ] AC5（R4.2/R4.3）写门关闭或 native 返回错误时，事件照常入库，`clip_id` 为 NULL。
- [ ] AC6（R4.4）启动时有残留 `recording` 行：文件存在 → `completed` + partial；不存在 → `failed`。
- [ ] AC7（R5.4）被告警引用的片段有 `.lock`，storage 的 TTL 和水位清理都不删它；只被识别引用的片段过期后被删，`event_clips` 行随之删除，识别行 `clip_id` 变为 NULL。
- [ ] AC8（R6）浏览器真实环境测试：H.264 片段能在详情里播放并定位到偏移；mock 掉 codec 支持探测后显示下载提示。
- [ ] AC9 `make check` 通过；`make cross-board` 能编译；按 AGENTS.md 加进 `api-docs` 白名单；相关 spec 已更新。

## Out of Scope

- 连续（7×24）录像、录像时间轴回放
- 抓拍事件触发录像
- 音频
- 转码 / 在服务端把 H.265 转成 H.264；前端用 WebCodecs/WASM 回放 HEVC
- 回放时叠加检测框
- 独立的「事件录像」列表页
- 向 EventHub / alertpush 推送「片段完成」
- 片段封面缩略图

## Open Questions

- （无阻塞项）具体数值（32 MiB / 120s）以 RK3568 压测为准，可在实现阶段调整默认值，不改变行为。
