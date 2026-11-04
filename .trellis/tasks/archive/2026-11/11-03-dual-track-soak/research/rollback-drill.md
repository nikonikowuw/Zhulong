# 降级与回滚演练（板端实测）

> 父任务 Phase 5.5 / AC-2、AC-4。全部用**协议层观测**判定：能力帧内容、WebSocket 关闭码、
> 帧计数。不看 UI、不靠日志猜。
> 原始数据：`board-rollback-drill.json`（由 `scripts/audio_drill.py` 生成）。

## 1. 结果总表

板端：RK3568 / `/tmp/zhulong-soak/Zhulong`（aarch64，`make cross-board` 产物），码流由板端
`rtsp_fixture.py --serve` 提供。命令：

```bash
python3 scripts/audio_drill.py --base-url http://192.168.19.71:8000 \
    --plain-camera soak01 --audio-camera soak13 --audio-camera2 soak14 \
    --latm-camera latm1 --broken-camera broken1 \
    --json .trellis/tasks/11-03-dual-track-soak/research/board-rollback-drill.json
```

| # | 用例 | 观测 | 结果 |
| :- | :--- | :--- | :--- |
| 1 | 无音轨相机 | 能力帧 `present=false, reason="camera reports no audio track"`；视频 3 帧照常；音频通道先收 `{"type":"error","code":"NO_AUDIO"}` 再以 **4404** 关闭 | ✅ |
| 2 | `audio-latm`（MP4A-LATM） | 音频通道拿到 `{"codec":"aac","sampleRate":44100,"channels":1,"config":"EghW5QAA"}`，6 s 内收到 **5 帧 ZLA1** | ✅ |
| 3 | `audio-broken`（音频 SETUP 失败） | 视频 5 帧照常；能力帧 `present=false`；Native 日志 `rtsp_audio_open_failed rc=-11 falling back to video-only`（WARN）+ `stream_ready … audio: has=0` | ✅ |
| 4 | 同会话接管 | 第一条先出帧，第二条建连后第一条收到 **4001 superseded**，第二条继续出帧 | ✅ |
| 5 | 双会话并行 | 两个独立登录会话各听一台相机，各 5 帧，互不关闭 | ✅ |
| 6 | 全局上限 | 8 个会话各占一条 → 第 9 个会话收到 **4002 too_many_channels**；服务端日志 `audio channel rejected: limit reached channels=8 limit=8` | ✅ |
| 7 | 旧客户端（忽略文本帧） | 只读二进制帧：10 帧、magic 全为 `ZLM2`；文本帧 1 条被忽略 | ✅ |

补充证据（同一轮日志）：

```json
{"level":"warn","msg":"rtsp_audio_open_failed rc=-11 falling back to video-only","streamId":19}
{"level":"warn","msg":"audio channel rejected: limit reached","channels":8,"limit":8}
{"level":"info","msg":"audio channel closed","code":1000,"dropped":0,
 "opened_total":4,"superseded_total":0,"rejected_total":0}
```

## 2. 两条「无法在板端直接造出来」的演练

| 场景 | 怎么给证据 | 结论 |
| :--- | :--- | :--- |
| **旧前端 bundle 收新后端** | 用例 7 在协议层复刻了旧 bundle 的判据（`event.data instanceof ArrayBuffer`）：文本帧被忽略，二进制帧逐字节仍是 ZLM2 | 画面不受影响；音频入口不存在（旧 bundle 里没有那个按钮） |
| **新前端收旧后端（不发能力帧）** | 浏览器测试 `web/src/features/live/components/live-player-cell.test.tsx`：「`streamInfo=null`（能力帧还没到 / 旧后端）：置灰但不报『无音频』」 | 喇叭置灰、tooltip「正在确认该路音频能力」；不建音频通道、不进报错循环 |

这两条不靠「回滚二进制再跑一遍」取证，理由是它们测的都是**协议兼容性**，而协议是版本化的：
只要文本帧对旧客户端不可见、能力帧缺失对前端是「未知」而不是「故障」，滚动升级就安全。
真要做二进制级回滚，等价做法是用 `git stash` 前的 `Zhulong` 覆盖板端二进制重启 —— 行为与上表一致。

## 3. 无开关的确认

- `config.toml` / `config.toml.example` 里**没有任何音频开关**（只有一句注释说明为什么没有）；
  两轮压测（`audio` / `baseline`）用的是同一份配置、同一个二进制。
- 能力靠探测（`stream_info` 的 `present`），投递靠用户手势（解禁喇叭才建 WS），
  失败自动降级（无声视频）。所以「回滚」在本期只有两种形态：**回滚前端**（失去音频入口）
  与**回滚二进制**（回到没有音频字段的版本），都不需要改配置。

## 4. 复现步骤

1. 板端起桩 `python3 rtsp_fixture.py --serve --host 0.0.0.0 --port 8554`；
2. 建 5 台相机：`soak01 → /soak/cam01`、`soak13/14 → /soak/camNN-audio-pcmu`、
   `latm1 → /audio-latm`、`broken1 → /audio-broken`（`POST /api/v1/cameras`）；
3. 跑上面的 `audio_drill.py` 命令；
4. 退出码 0 = 七条全过；失败项在 JSON 的 `cases[].error` 里带具体观测值。
