# 研究摘要：裁剪视图下的算法插件契约

> 本文件是 `.trellis/spec/native/algo-plugin-contract.md`（63KB）中与“帧视图是分析窗口”
> 相关条款的任务级摘要。完整规范仍以原文件为准。

## 1. 算法包看到什么

- 裁剪发生在算法进程**之前**：`zhulong_frame_view` 的 `width/height/planes/strides/dma_buf_fd`
  全部是窗口视图；`raw_view()` 与 `Image::width()/height()` 都指向同一张窗口图。
- 算法包**不需要**知道 crop 的存在，也不需要 `supports_crop` 能力位。
- 旧包（未重建）必须继续工作：它收到的就是一张完整的小图，坐标语义与以前一致。
- `sdk_version`、`IPC_VERSION`、`zhulong_frame_view` 布局一律不变。

## 2. 像素阈值与几何假设

- 像素阈值（最小人脸、最小车牌、模糊阈值等）必须以 `image.width()/height()` 为基准，
  不得假设相机原始分辨率或固定宽高比。
- 非 16:9 窗口会让算法看到不同形状的输入；假设固定宽高比的算法必须改成按 `width/height`
  计算，或明确在窗口内失效。
- letterbox 的 `scale/pad` 回填与 de-letterbox 必须以**同一张窗口图**为基准；
  不得再用相机全幅尺寸。
- `raw_view()` 的 `strides/planes` 是窗口紧打包布局；RGA 预检失败时算法侧照常回退 CPU。

## 3. capture_time 与结果回传

- `FrameDispatchMsg.capture_time_ms` 由宿主在**视图快照点**生成；算法骨架
  （`native/sdk/src/algo_main.cpp`）已经把它原样写进结果 JSON，不需要改包。
- 算法包若自行覆盖 `capture_time_ms`，会破坏宿主按 `effective_since_ms` 选窗；
  契约必须明确“原样回传，不得替换为模型完成时间/帧 PTS”。
- 结果坐标仍是窗口局部归一化 `[0,1]`；宿主统一逆映射，算法不得自己换算全幅。

## 4. 生命周期与内存

- 帧视图指针只在 `process()` 同步调用期间有效；异步持有必须深拷贝。
- `dma_buf_fd` 是客户端进程本地副本；不得把 dispatch 的 `reserved_fd` 当 fd 用。
- `planes[0..2]` 由 `offsets` + `strides` 定位；窗口帧的 `offsets[0]` 为 0。
- 结果内存由插件创建、`result_release` 释放；宿主的窗口拷贝不改变这条所有权链。

## 5. 验证点

- 用未修改的 face/plate/crowd 包在裁剪流上跑 e2e：输出框回到全幅后应落在真实目标上。
- 合成图案（四象限 + 非对称角标）断言模型输入 == 窗口内容。
- 旧包（无 `supports_crop`、无新字段）必须零改动通过；这是 AC9 的核心。
