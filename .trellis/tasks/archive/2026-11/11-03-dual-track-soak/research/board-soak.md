# 板端压测：16 路预览 × 30 分钟（含 4 路收听音频）

> 父任务 Phase 5.4 / AC-5。目标是把「不收听音频 = 今日行为」从口头承诺变成可复现的对照数据，
> 并给「收听中的音频通道不会拖累视频」一个量化结论。

## 1. 环境

| 项 | 值 |
| :--- | :--- |
| 设备 | RK3568（4 × Cortex-A55，1953 MiB RAM），`linaro-alip` / Debian 11，kernel 5.10.226 |
| 宿主二进制 | `make cross-board` 产物 `build/targets/rk3568-rknn-rga/Zhulong`（aarch64，71 MB），部署于 `/tmp/zhulong-soak/` |
| 配置 | `http.address=0.0.0.0:8000`，`data.directory=/tmp/zhulong-soak/data`，`decode.backend=mpp`、`allow_software_fallback=false`、`max_sessions=8`，`algorithm.target=rk3568-rknn-rga` |
| 码流源 | 板端 `native/tests/rtsp_fixture.py --serve --host 0.0.0.0 --port 8554`（真实 RTSP/RTP over TCP 回环，25 fps / 640×360 H.264；带音频的档位是 G.711 μ-law，50 pps） |
| 客户端 | 宿主机 `scripts/soak_preview.py`（16 条预览 WS；音频轮次再开 4 条音频 WS，每路一个**独立登录会话** —— 同一会话第二条会 4001 接管，测到的就不是并发了） |
| 采样 | 板端 `/tmp/zhulong-soak/sample.sh` 每 5 s 读 `/proc/<pid>/{stat,status}`（CPU tick / VmRSS / 线程数 / loadavg） |
| 口径 | `CLK_TCK=100`；CPU% = Δticks ÷ 100 ÷ Δt × 100（**单核**百分比，4 核满载 = 400%） |

两轮各 1800 s，唯一差别是「有没有客户端订阅音频」：

| 轮次 | 预览 | 音频通道 | 起始（UTC） | 原始数据 |
| :--- | ---: | ---: | :--- | :--- |
| A（audio） | 16 | 4（soak13~16，各自独立会话） | 2026-11-04 03:44:28 | `board-soak-audio.json` |
| B（baseline） | 16 | 0 | 2026-11-04 04:17:42 | `board-soak-baseline.json` |

复现命令：

```bash
# 板端（一次性）
rsync -az build/targets/rk3568-rknn-rga/Zhulong /tmp/zhulong-soak/config.toml \
    native/tests/rtsp_fixture.py rk3568:/tmp/zhulong-soak/
ssh rk3568 'cd /tmp/zhulong-soak && (setsid nohup python3 rtsp_fixture.py --serve --host 0.0.0.0 --port 8554 &)'
ssh rk3568 'cd /tmp/zhulong-soak && (setsid nohup ./Zhulong > zhulong.log 2>&1 &)'
ssh rk3568 "cd /tmp/zhulong-soak && (setsid nohup ./sample.sh \$(pgrep -x Zhulong) 1900 metrics.csv &)"

# 宿主机（每轮）
python3 scripts/soak_preview.py --base-url http://192.168.19.71:8000 \
    --preview 16 --audio 0|4 --duration 1800 --window 30 \
    --label baseline|audio --json .trellis/tasks/11-03-dual-track-soak/research/board-soak-<label>.json
```

相机由 `POST /api/v1/cameras` 建 16 路，`soak01~12` 指向 `/soak/camNN`（纯视频），
`soak13~16` 指向 `/soak/camNN-audio-pcmu`（视频 + G.711）。

## 2. 客户端侧：帧与协议

| 指标 | A（4 路收听） | B（不收听） | 差异 |
| :--- | ---: | ---: | :--- |
| 预览连接 | 16 | 16 | — |
| 预览帧数（29.9 min） | 712 305 | 712 338 | **+0.005 %** |
| 预览帧率（16 路合计 / 单路） | 395.6 /s（24.7 /s） | 395.7 /s（24.7 /s） | — |
| 预览负载带宽 | 281.7 kbit/s | 281.7 kbit/s | — |
| **`seq_gaps`（真实丢帧）** | **0** | **0** | — |
| 协议错帧 | 0 | 0 | — |
| 非正常关闭（除 1000/1001） | 0 | 0 | — |
| 首帧耗时（16 路最大） | 122.1 ms | 88.8 ms | — |
| 音频连接 | 4 | 0 | — |
| 音频帧数 / 帧率 | 353 863（196.6 /s，单路 49.2 /s） | — | — |
| 音频负载带宽 | 251.6 kbit/s | — | — |
| `discontinuities` / `synthetic_pts` | 0 / 0 | — | — |
| 能力帧下发 | 16 条（其中 4 条 `{pcmu, 8000 Hz, 1ch}`，12 条 `present=false`） | 同左 | — |

音频 49.2 帧/s ≈ 50 pps（20 ms G.711 包）—— 与源码率一致，既没有累积延迟也没有重排。
服务端关闭日志同样干净（4 条通道各一行）：

```json
{"msg":"audio channel closed","code":1000,"seq":88468,"dropped":0,
 "opened_total":4,"superseded_total":0,"rejected_total":0}
```

## 3. 板端进程：CPU / RSS / 线程

统计窗口**两轮对齐**为 `t=30 s … 1796 s`（跳过起流阶段，截止于客户端断开）：

| 指标 | A（4 路收听） | B（不收听） | 差异 |
| :--- | ---: | ---: | :--- |
| 进程 CPU（单核百分比） | **41.4 %** | **37.0 %** | **+4.4 pp**（≈ +1.1 pp / 收听通道） |
| 折算到 4 核整机 | 10.4 % | 9.3 % | +1.1 pp |
| 稳态 RSS 起→止 | 104 → 105 MB | 99 → 105 MB | 同为平台值，无增长 |
| RSS 斜率（t≥120 s） | −0.18 MB/min | −0.22 MB/min | 无增长 |
| 线程数 | 28 → 29 | 27 → 28 | +1（4 条音频共用少量线程） |
| 系统 load1 量级 | 0.6 ~ 1.1 | 0.4 ~ 0.5 | — |

RSS 轨迹（A）：0 s 53.4 MB（纯宿主）→ 30 s 104.6 MB（16 路流建立）→ 1801 s 105.9 MB。
**30 分钟没有单调增长**：4 条音频通道（各 48 帧发送队列）没有泄漏迹象。

## 4. 结论

- **音频对视频侧零影响（有量化证据）**：16 路预览的帧数、帧率、带宽、`seq` 空洞、
  协议错帧在「4 路收听」与「完全不收听」两轮中完全一致（差值 0.005 % 属噪声）。
- **代价是 CPU，而且是可量化的**：4 条收听通道带来 **+4.4 pp 单核**（≈ 1.1 pp / 通道，
  占整机 4 核的 1.1 %）。这部分成本主要来自 G.711 包的复制与 WebSocket 写，
  不含任何音频解码（原生层只解复用）。
- **RSS 无增长**：两轮都是 30 分钟内平台值，斜率朝下。
- 降级路径（无音轨、LATM、SETUP 失败、接管、上限）见 `rollback-drill.md`。

## 5. 观测到的两件事（建议单开任务）

1. **warm 分发器上限的实际行为与常量不一致**：`defaultMaxWarmStreamDispatchers = 4`，
   但 16 条客户端在同一秒断开后，**16 条分发器都在 04:14:31 被释放**
   （`stream dispatcher closed` × 16、`stream dispatcher counters final` × 16），
   而不是只淘汰 12 条、保留 4 条热流 10 分钟。影响：用户切页回来要重付一次 RTSP 建连（1~2 s）。
   怀疑 `warmOrder` 里存在同一 key 的重复入列，导致淘汰计数与真实保留数不一致。
   与音频无关，属 pre-existing。
2. **分发器计数曾是死代码**：`StreamDispatcher.Counters()` 没有任何调用者，压测需要的
   `GOPTruncations` / `LagResyncs` 只能靠猜。本次接上了周期（5 min）与关闭时两条日志；
   本轮 16 路全程 `gop_truncations=0 / lag_resyncs=0 / queue_resyncs=0 / resync_skips=0`，
   音频通道 `dropped=0`。

## 6. 原始数据

- `board-soak-audio.json` / `board-soak-baseline.json`：每条连接的帧数、字节数、`seq` 空洞、
  首帧耗时、音频能力帧、关闭码，以及每 30 s 一格的 `windows` 时间序列（可画漂移曲线）。
- 板端 CSV（`metrics-{audio,baseline}.csv`，每 5 s 一行）未入库（临时目录），
  采集脚本与命令见 §1，第三人可原样重跑。
