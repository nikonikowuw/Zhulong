# 音画同步实测：白闪 + 1 kHz beep（10 分钟）

> 父任务 AC-3：「稳态 ≤ 80 ms；视频重同步后 500 ms 内回收；10 分钟不发散」。
> 判定口径与测量边界见文末 —— 先说清楚它测的是什么，再说数字。

## 1. 结论

| 判据 | 目标 | 实测（600 s） | 结论 |
| :--- | :--- | ---: | :--- |
| 稳态 \|Δ\| 中位数 | ≤ 80 ms | **28.9 ms** | 通过 |
| 稳态 \|Δ\| p90 | （记录项） | 66.98 ms | — |
| 重同步后（≥500 ms）\|Δ\| 中位数 | ≤ 80 ms | **30.7 ms** | 通过 |
| 重同步后 \|Δ\| 最大值 | ≤ 80 ms | 78.58 ms | 通过（余量 1.4 ms） |
| 10 分钟漂移（首 1/10 与末 1/10 的中位数之差） | ≤ 20 ms（design §6.3） | **4.0 ms** | 通过 |
| 样本数（白闪 / beep / 配对） | — | 295 / 294 / **294** | — |

符号约定：Δ = 音频起播时刻 − 视频绘制时刻；**负值表示声音比画面早**。本轮中位数为
**−28.9 ms**，即稳定地「声音略早于画面」。这个偏移量级来自「音频锚点按首包到达时刻建立」
（`base = audio_arrival − video_origin_arrival`）这个设计选择，而不是逐帧抖动：
10 分钟内首尾差值只有 4 ms。

## 2. 环境与命令

| 项 | 值 |
| :--- | :--- |
| 浏览器 | Playwright 驱动的 Chromium（Vitest browser mode），真实 WebCodecs H.264 软解 + AudioWorklet |
| 宿主后端 | 本机 `build/Zhulong`，`:18000`，独立数据目录 |
| 码流源 | `native/tests/rtsp_fixture.py --serve --port 8554 --sync-clip /tmp/zhulong-sync` |
| 相机 | `cam4` → `rtsp://127.0.0.1:8554/sync` |
| 素材 | `native/tests/make_sync_clip.sh`（640×360@25 fps，每 2 s 白闪 80 ms；8 kHz μ-law，每 2 s 1 kHz/100 ms beep） |

```bash
# 1) 素材 + 桩
native/tests/make_sync_clip.sh /tmp/zhulong-sync 16
python3 native/tests/rtsp_fixture.py --serve --port 8554 --sync-clip /tmp/zhulong-sync &

# 2) 宿主（独立数据目录，避免和 `make run` 抢 8000）
mkdir -p /tmp/zhulong-sync-run/data
sed 's|^directory = "./data"|directory = "/tmp/zhulong-sync-run/data"|' config.toml \
    > /tmp/zhulong-sync-run/config.toml
(cd /tmp/zhulong-sync-run && /path/to/build/Zhulong &)
# 初始化管理员并建 cam4 → rtsp://127.0.0.1:8554/sync

# 3) 结果收集器（Vitest 在浏览器模式下会吞掉通过用例的 console 输出）
python3 scripts/sync_result_collector.py --port 18082 --out /tmp/sync-result.json &

# 4) 测量（10 分钟）
cd web
VITE_SYNC_MEASURE=1 VITE_SYNC_CAMERA=cam4 VITE_SYNC_DURATION_S=600 \
VITE_SYNC_RESYNC_AT_S=120 VITE_SYNC_RESULT_URL=http://127.0.0.1:18082/sync \
ZHULONG_API_TARGET=http://127.0.0.1:18000 \
pnpm exec vitest run --browser.headless src/features/live/sync-measure.test.ts
```

用例本身（`web/src/features/live/sync-measure.test.ts`）默认跳过，只有
`VITE_SYNC_MEASURE=1` 时才连真后端。它复用**产品代码**的
`StreamDecoder` / `PresentationScheduler` / `connectAudioChannel` /
`createAudioFrameDecoder` / `createLiveAudioPlayer`，不注入探针、不改产品逻辑。

## 3. 原始结果

```json
{"camera":"cam4","durationS":600,"videoReconnects":1,
 "counters":{"binary":14947,"parseErrors":0,"decodeRejected":24,"decodedFrames":14922},
 "samples":{"flashes":295,"beeps":294,"pairs":294,"afterResync":239},
 "steadyStateMs":{"medianAbs":28.925,"p90Abs":66.975,"medianSigned":-28.925},
 "afterResyncMs":{"medianAbs":30.675,"maxAbs":78.575},
 "driftMs":{"firstTenthMedian":-27.375,"lastTenthMedian":-23.375,"delta":4},
 "audioInfo":{"codec":"pcmu","sampleRate":8000,"channels":1,"config":""},
 "player":{"droppedStale":152,"droppedAhead":0,"rebased":3,"played":29854,"held":0,"degraded":false},
 "missedBeepOnsets":2}
```

逐项解读：

- **`decodedFrames` 14 922 / 600 s = 24.9 fps**：与源的 25 fps 一致，说明「解封装 → 解码 → 呈现」
  整条链路没有降帧。
- **`decodeRejected` 24**：人为断线重连那一次，解码器等新 IDR 期间丢掉的非关键帧
  （24 帧 ≈ 1 s，正是重连等待窗口）。这是**预期**行为，不是丢帧。
- **`player.rebased` 3**：起播 1 次 + 断线重连时 `flush()` 1 次 + 一次环深/陈旧触发。
- **`droppedStale` 152 / `played` 29 854**：0.5 %；对应 2 个 beep 起点（`missedBeepOnsets`）。
  起播与重连瞬间的陈旧帧被丢弃，符合 `live-audio-player` 的 250 ms 陈旧阈值。
- **`afterResync` 239 个样本**：重连后 1 s 起算，横跨余下 ~8 分钟，说明「重同步后回收」不是
  只在一个点上成立。

## 4. 测量边界（必须说清楚）

测的是**渲染器调度层**的偏差：

- 视频侧：白闪帧真正 `drawImage` 到 canvas 的 `performance.now()`；
- 音频侧：该 beep 帧被 `live-audio-player` 排进 AudioContext 输出时间轴的 `startDueMs`
  （`onScheduled` 钩子，含 `PRE_ROLL` 与重锚后的真实起点）。

**不包含**：面板扫描/合成器呈现延迟、声卡/DAC 缓冲、蓝牙耳机等设备固定延迟。
design §6.3 的「240 fps 录屏 + 采集卡」是端到端定义，需要真实屏幕与采集硬件；
本环境没有这两样，因此这里给出的是「代码负责的那一段」的证据，而不是伪造一个端到端数字。
设备延迟对两条轨道是**共同**延迟，不影响「相对偏差 ≤ 80 ms」这一判据；
若日后要做端到端复核，方法同上，只是把两侧换成一个同帧录制的采集卡。

## 5. 顺带修掉的一个真 bug（否则这条测量根本跑不起来）

首次运行时 Chromium 报：

```
decoder_error {"errorName":"OperationError",
 "errorMessage":"Unsupported configuration. Check isConfigSupported() prior to calling configure()."}
```

根因是 `VideoDecoderConfig.hardwareAcceleration = 'prefer-hardware'` 在**没有硬件解码器**的
环境（无头 Chromium、纯软件桌面、部分 WebView）不是「软降级」而是 **异步** 报错：
`configure()` 不抛异常，错误回调随后才到。旧实现只 `try/catch`，于是陷入
「同一份配置反复重建解码器」的循环而不自知 —— 真实用户在无 GPU 的机器上会看到黑屏。

修复（`web/src/features/live/services/webcodecs-decoder.ts`）：

1. 记住当前硬件偏好，首次 `configure` 用 `prefer-hardware`；
2. 错误回调里识别 `NotSupportedError` / `Unsupported configuration`，降级为 `no-preference`
   后重建，不再重复试硬件；
3. `configure()` 同步抛错的浏览器（规范路径）仍保留第一层 `try/catch` 回退。

回归用例：`webcodecs-decoder.test.ts` 两条（同步抛错回退 / Chrome 式异步错误降级）。
