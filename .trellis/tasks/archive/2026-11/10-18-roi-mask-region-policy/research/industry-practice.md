# 行业实践摘要：ROI / MASK 区域归属判定与结构

> 本文件是本任务的关键结论摘要，供规划与评审快速核对。
> 完整原文见同目录 `*.txt`（由官方文档抓取，抓取日期 2026-10-18）。

## 1. 区域归属判定点：底部中心（bottom-center）

三家主流实现一致采用**目标框底部中心单点**判定，且**重叠面积不参与**：

| 产品 | 原文要点 | 来源 |
| :--- | :--- | :--- |
| NVIDIA DeepStream (nvdsanalytics) | "For all the analytics calculations **bottom center coordinate** of bounding box of an object is being used... This behavior applies to **all analytics features — including ROI-based detections**" | `deepstream_nvdsanalytics.txt`；<https://docs.nvidia.com/metropolis/deepstream/dev-guide/text/DS_plugin_gst-nvdsanalytics.html> |
| Frigate | "Presence in a zone is evaluated based on the **bottom center of the bounding box** for the object. **It does not matter how much of the bounding box overlaps with the zone.**" | `frigate_zones.txt`；<https://docs.frigate.video/configuration/zones/> |
| Axis (Object Analytics) | "An object's point of detection must be inside the detection area. For humans, this point is at the **feet**. For vehicles, it's at the **center**." | `axis_aoa.txt`；<https://help.axis.com/en-us/axis-object-analytics> |

**不可配置**：DeepStream 社区请求自定义该判定点时，官方回复「currently nvdsanalytics does not support customize the bottom center coordinate」（`nv_forum_coord.txt`；<https://forums.developer.nvidia.com/t/customizing-object-coordinate-for-roi-detection-in-nvdsanalytics/330705>）。

## 2. ROI 与 MASK 的结构

| 产品 | 结构 | 说明 |
| :--- | :--- | :--- |
| Axis | **1 个 include area + 最多 5 个 exclude areas** | exclude area 是「**在 include area 内部**挖掉不检测的区域」，专治区域内高误报点。include 默认矩形，可改为最多 10 角多边形 |
| DeepStream | 1 个 ROI 列表 + `inverse-roi` 布尔 | 仅支持「区域内」或「区域外」二选一，表达力弱于两列表 |
| Hikvision | 最多 4 个虚拟区域 | 另配灵敏度、最大/最小目标尺寸、停留时间阈值 |
| Frigate | `zones`（include 语义） + `masks`（**像素级运动掩码**） | 与结果级排除是两回事 |

**结论**：Axis 的「include + exclude 两列表」与本需求「ROI 保留 / MASK 丢弃」最吻合；两列表模型严格强于 DeepStream 的单列表取反。

## 3. 术语陷阱

行业中 "mask" 有两种不同含义，**不可混用**：

1. **像素级隐私/运动掩码**（Frigate `masks`）：修改画面或抑制运动检测，属图像域。
2. **结果级排除区**（Axis `exclude areas`）：丢弃落在区域内的检测结果，属结果域。

本需求是**第 2 种**。UI 文案与字段命名须避免让用户误以为是隐私遮挡。

## 4. 单点判定必须配时间防抖

- Frigate 的 "zone inertia" 文档直陈原因：*"an object's bounding box may be slightly incorrect and the bottom center of the bounding box is inside the zone while the object is not actually in the zone"*，故要求目标**连续多帧**在区域内才算进入。
- Frigate 另有 `loitering_time`；Hikvision 有 Time Threshold（停留时长）与 max/min size 目标尺寸门限。

**本项目的对应措施**：`internal/record/service.go` 已有按 track 的冷却去重，覆盖基础时间防抖；区域惯性列为 Out of Scope。

## 5. 对本任务设计的影响

| 决策 | 选择 | 依据 |
| :--- | :--- | :--- |
| 判定点 | 目标框底部中心 | 三家一致（§1） |
| 重叠面积 | 不参与判定 | Frigate 明示（§1） |
| 结构 | ROI 与 MASK 两个列表 | Axis 模型（§2） |
| 优先级 | MASK 优先排除 | Axis「exclude 在 include 内部挖掉」（§2） |
| 命名 | 避免用「隐私掩码」措辞 | §3 |
| 时间防抖 | 复用既有 track 冷却 | §4 |
