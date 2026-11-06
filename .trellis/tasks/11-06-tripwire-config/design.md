# 设计

扩展 internal/region.Config 与前端区域草稿的 tripwires；独立线画布与列表集成已有区域编辑器/撤销栈。方向由有序端点定义：屏幕坐标叉积为负是 A，正是 B。最小归一化线长 .01，最多8条，ID限64 ASCII字符，名称限64 Unicode字符。

保留 MSG_TASK_CONFIG 原文；新增 MSG_TASK_REGIONS=15，复用68字节目标算法+JSON长度头。宿主可选 TaskRegionProvider 接口通过现有配置播种时机发送完整 region JSON。Broker 按消息类型和算法缓存，握手重放，修复原有单份配置被多算法覆盖的问题。新 SDK 将区域原文放在 TaskContext.regions.json，内容变化递增 revision；旧包忽略新消息。

FrameDispatchMsg 尾部追加实际 full-frame view rect，保留96/168字节前缀兼容。SDK 提供坐标转换，允许裁剪外坐标越界，不做错误clamp。新增字段影响C++ SDK布局，版本升1.2.0，插件C ABI与IPC版本不变，包需重建使用新能力。

Go借用字节同步跨CGO后由C++ Broker复制拥有；SDK区域字符串由worker拥有，string_view仅在同步推理调用有效。
