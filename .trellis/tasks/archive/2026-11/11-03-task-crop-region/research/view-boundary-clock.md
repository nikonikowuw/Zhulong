# 研究摘要：裁剪窗口的边界时钟（R3.1 / R3.2）

> 目标：让“窗口局部像素”与“Host 逆映射用的 E”在换窗时严格对齐，且不要求旧算法包重建。
> 结论：`capture_time_ms` 提前到**视图快照点**，宿主用引擎回报的 `effective_since_ms` 选上一代/当前 E。

## 1. 为什么不能用 Go 本地改窗时间

- `SetView` 是 Host 同步调用，但 `FramePublisher` 可能已经持有旧 view 的帧，稍后才发布；
  该帧的 `capture_time` 会晚于本地 `time.Now()`，被误判成新窗口。
- 分辨率重协商时，Host 要到下一批结果才看见新 `E`；新窗口帧已经发布，旧 `E` 会误映射。
- 结果 JSON 没有 `frame_id`/`view_epoch`，Host 无法事后判断一帧属于哪个窗口。

## 2. 选定方案

```text
FramePublisher:
  snapshot = stream.view_snapshot(frame.width, frame.height)
                                      // 同一把 Stream::mutex 内：
                                      //   几何按本帧尺寸求解（与缓存不同则重算 E）
                                      //   capture = max(now, last_capture)
                                      //   last_capture = capture
                                      //   几何变化 / set_view 变化 → since = max(now, last_capture + 1)
  publish_frame(..., capture)

Stream::set_view（同一把锁）:
  有几何缓存 → 立即按缓存尺寸重算 E，
    effective_since = max(now, last_capture + 1)
    last_capture = effective_since   // 必须推进，否则改窗后的第一帧仍可能 < since
  无缓存（尚未出帧）→ 只记 pending_rect_，返回 NOT_READY

Stream::on_ready:
  只更新元数据/日志，不算几何；尺寸变化由下一次 view_snapshot 落地
```

> R3.2：几何的输入必须是**解码帧尺寸**。用 RTSP 元数据（`codecpar`）算 `E` 会在 1088 裁切、
> MPP 对齐、分辨率热切换时产生偏差，甚至 `y+h > frame.height` 导致每帧 `fail_publish`。

- 旧窗口帧：`capture < effective_since`；
- 新窗口帧：`capture >= effective_since`；
- 同一毫秒内改窗也成立（`last_capture+1` 强制推进）。
- `set_view` 在有几何缓存时返回生效的 `ViewState{Rect, Since}`；无缓存时返回 `NOT_READY`，
  Host 置 `viewPending`，首个结果批次用 `GetView` 收敛（此时几何已按首帧尺寸落地）。
- Host 结果 JSON 里的 `capture_time_ms` 来自 dispatch，旧包已原样回传；Host 只比较
  `res.Timestamp` 与 `GetView()` 返回的 `Since`。
- `GetView` 每条结果刷新一次；失败保留当前视图。

## 3. 为什么不加 `view_epoch`

- 旧包不会回传新字段，AC9（旧包零改动）会失效。
- 不加字段就能让旧包继续工作，因为 `capture_time_ms` 本来就在 dispatch 里且骨架会回传。
- 代价是 `capture_time_ms` 的语义从“broker 发布时钟”改成“宿主视图快照时钟”，
  AC1 要求“字段/布局/内容路径一致”，`capture_time_ms` 毫秒值允许与旧实现有差异。

## 4. 生命周期与共享流

- 视图属于 `Stream`，不是 AI 订阅。AI 全撤但 preview/recording 仍持有同一个 `Stream` 时，
  `Stream` 不销毁，视图也不会自动消失。
- 最后一次 AI 订阅释放时必须 `SetView(NULL)`；下一次 AI acquire 必须按 DB 当前 crop 重放。
- `acquire` 顺序：`awaitReady → provider.CropFor → SetView → 注册 aiStreamRef →
  SubscribeAIResults → 启动 consumer → 返回 broker path`。
  这样首批结果不会在“视图还未设置”时被消费。

## 5. 已知限制

- Host 只保留一代 `viewPrev`；如果两次窗口变化之间完全没有结果批次/`GetView`，
  中间一代可能不可见。任务保存/分辨率重协商的正常频率下不会发生。
- 若未来需要强保证，再让 `get_view` 返回最近 N 代，或引入 result epoch；本期不做。
- 所有时间戳使用宿主 `system_clock` 毫秒；NTP 大幅回拨时靠 `max(last+1)` 保持单调，
  但跨时钟域的强一致不在本期范围。

## 6. 验收测试

- `capture_time < since` / `== since` / `> since` 三条；
- 同一毫秒内 `set_view`；
- 旧包（未改）在 `set_view` 前后各一条结果，框落到正确全幅位置；
- 共享 preview 的 Stream：AI 全撤后 `SetView(NULL)`，再 acquire 重放；
- `GetView` 失败/`NOT_FOUND` 时保留当前视图，不清空。
