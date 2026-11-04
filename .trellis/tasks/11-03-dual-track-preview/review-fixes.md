# 评审修复：native 双轨音频（ABI v5）

> 来源：对 `native/` 双轨实现的代码评审（非任务 PRD 的新需求）。
> 只改与音频双轨相关的行为与可观测性；不新增 ABI 字段，不改变 v5 布局。

## 1. 已修

| # | 问题 | 修复 | 证据 |
| :- | :--- | :--- | :--- |
| 1 | 音频降级重试 `expire_after` 重新计时，失败耗时 2×open_timeout，突破 Go `Acquire` 折叠的 ctx deadline；`TIMEOUT`/`UNSUPPORTED` 也被当成“音频失败”重试 | 打开阶段改用单一绝对截止点（`Cancellation::expire_at`）；降级只对 `ERR_IO` 且预算未花完时触发，第二次握手不重新计时 | `audio_fallback_budget_case`：修复前实测 2006 ms，修复后 1010 ms（`open_timeout=1000`）。已做负向验证（临时回退 → 用例红在 `elapsed_ms < 1800`） |
| 2 | 音频锚点/丢弃计数无任何出口，“有音轨没声音”现场不可判 | `dispatch_audio` 锚点建立时打一次性 `audio_anchor_established base_us= dropped_before_origin=` | `Stream::audio_anchor_logged_`；每路流一行 |
| 3 | 首包缺 PTS 时锚点落在合成轴上，恢复真实 PTS 后输出前跳（单调保护只拦回拨） | 首枚真实 PTS 重锚一次并置 `DISCONTINUITY` | `TestReanchorOnFirstRealPts`（200 s 的绝对 PTS 只映射为 base，不瞬移） |
| 4 | AAC extradata > 256 B 被静默丢弃但仍宣告 `has_audio=1` | 超限落 `rtsp_audio_extradata_too_large size= limit=`；上限提为具名常量 | `rtsp_input.cpp`；`max_audio_extradata_bytes` |
| 5 | 音频 `dts_us` 有效但 `has_dts=0`，且不置 `DTS_SYNTHETIC` | 缺源 DTS 时置 `ZHULONG_PKT_FLAG_DTS_SYNTHETIC` | `TestDtsSyntheticFlag` |
| 6 | 纯 C ABI 测试对 v5 零覆盖 | `_Static_assert` 钉 `pkt_flags`/`track_type` 偏移与结构大小；补 `get_abi_version`、`subscribe_track`、`audio_info` 的空参数/未运行断言 | `native/tests/engine_abi_test.c` |
| 7 | 8 KiB 包长上限为魔数 | `max_audio_packet_bytes`，超限日志带 limit | `rtsp_input.cpp` |
| 8 | `AudioNormalizer::reset()` 无调用者；`__int128_t` 无编译期守卫 | 删除死代码；照 `preview_normalizer.cpp` 加 `__SIZEOF_INT128__` 分支 | 编译全绿 |
| 9 | 文档与实现不一致 | `ingestion-contract.md §3.6`、`cgo-contract.md §4`、`native/README.md` 同步 | 见下 |

## 2. 口径澄清（不改代码）

- **`audio_info` 宽限期语义**：已 `release` 但仍在 8 秒宽限期的流仍返回 `OK`（物理连接还在，
  且只写调用方缓冲区、不借出内部指针）。父 PRD 的「流关闭后返回 NOT_FOUND」应按
  「流被回收后 NOT_FOUND」修正，写进 `cgo-contract.md §4`。不改成 release 即 NOT_FOUND：
  那会给 Go 层引入一个没有收益的行为变化。

## 3. 未闭环（不在本次范围）

- **父 PRD 的 AAC-LATM 口径**：`11-03-dual-track-preview/prd.md` R1.2 与 AC-1 仍写“明确拒绝 + WARN”，
  实现与测试钉的是“当裸 AAC 放行”。`11-03-dual-track-native/progress.md §3` 已记录，D 阶段必须改 AC。
- **`Stream::on_decode_fatal_error` 自死锁**（持 `Stream::mutex` 调 `request_stop()`，后者重入同一非递归 mutex）：
  非双轨、pre-existing，建议单开一条 bug 任务。
- **音频“宣告但从不发 RTP”**：目前只有人工看日志能发现，暂无看门狗。
- **`audio_startup_cost_case` 的 `+50ms` 墙钟门限**：仍是抖动风险，本次未动（回环两次均通过）。

## 4. 验证

```bash
make native-build && make native-test                                  # 31/31
python3 native/scripts/build.py --sanitizer address test               # 31/31
python3 native/scripts/build.py --sanitizer thread  test               # 31/31
python3 native/tests/rtsp_fixture.py build/native/host/ZhulongRtspTests # 含 budget 用例
```

`/audio-broken` 仍恰好 2 次连接（降级路径未被预算修复误伤），`/audio-pcmu` 仍 1 次。
