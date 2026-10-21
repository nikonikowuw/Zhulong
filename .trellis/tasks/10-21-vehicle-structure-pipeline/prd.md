# PRD: vehicle_structure 机动车结构化任务端到端体系与车牌识别算法插件

## Goal

在 Zhulong 全栈中新增第一类任务类型 `vehicle_structure`（机动车结构化感知），构建涵盖 **Native C ABI ⇄ Runner 进程 IPC ⇄ Go 宿主数据模型 ⇄ 抓拍持久化落库 ⇄ 前端实时视频流叠加** 的完整端到端体系，并以此为基准规范接入车牌识别算法（包含纯车牌检测退化保全与车身-车牌 1:N 级联结构）。

---

## Background & 现状分析

| 编号 | 现有事实 | 关联文件与约束 |
| :--- | :--- | :--- |
| F1 | 系统当前仅支持 3 种任务形态：`detection`、`classification`、`person_structure` | `native/sdk/include/zhulong/plugin.h`、`internal/aitypes/model.go` |
| F2 | 人体/人脸结构化采用单块连续 Arena 内存打包（Contiguous Arena Packing），8 字节严格对齐，单次 `std::free` 释放整棵结果树 | `native/sdk/include/zhulong/algo.hpp`、`.trellis/spec/native/algo-plugin-contract.md` |
| F3 | 独立子进程 Runner 负责加载插件动态库，通过 Unix Socket 发送标准 JSON 给 Go 宿主 | `native/src/algorithm/runner/runner.cpp` |
| F4 | Go 宿主通过 `StreamHub` 汇聚 AI 结果，一方面广播给 WebSocket/SSE 前端，一方面交付 `record.Service` 进行冷却去重并存入 `captures` 表 | `internal/camera/stream_hub.go`、`internal/record/service.go` |
| F5 | 算法元数据 `manifest.json` 强校验 `task_type` 枚举；前端与后端均对任务类型做校验 | `.trellis/spec/backend/algorithm-guidelines.md`、`web/src/features/algorithms/data/schema.ts` |
| F6 | 车牌识别等实际算法存在「纯车牌检测（仅检车牌）」与「整车+车牌级联（检整车挂载车牌）」两种落地形态 | 需定义严密的退化与级联挂载规约 |

---

## Requirements

### R1. Native 纯 C ABI 与结构体对齐契约
- `zhulong_result_type_t` 新增 `ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE = 4`。
- 定义 `zhulong_plate_item`：包含归一化 `box[4]`、置信度 `score`、车牌号 `plate_number[32]`、车牌颜色 `plate_color[16]`、车牌类型 `plate_type`，尺寸严格 72 字节，满足 8 字节自然对齐。
- 定义 `zhulong_vehicle_item`：包含 `track_id`、车身归一化 `vehicle_box[4]`、车辆置信度 `vehicle_score`、车型 `vehicle_type[32]`、车身颜色 `vehicle_color[16]`、挂载车牌数量 `plate_count`、挂载车牌数组只读借用指针 `const zhulong_plate_item* plates`，尺寸严格 88 字节，满足 8 字节自然对齐。
- 编译期静态断言 `static_assert` 强制校验尺寸与对齐。

### R2. C++17 SDK 门面与 Arena 内存打包
- 提供高层 C++ 实体：`zhulong::PlateItem` 与 `zhulong::VehicleItem`。
- 实现 `VehicleStructureResultAdapter`：将多级嵌套树打包至单块连续堆内存（Arena），`internal_token` 记录首地址，`result_release` 时单次 `std::free` 彻底释放整棵树，杜绝内存碎片与嵌套泄漏。
- 提供导出宏 `ZHULONG_REGISTER_VEHICLE_STRUCTURE_PLUGIN(PluginClass, Name, Version)`。
- **纯车牌退化语义保全**：当插件仅运行车牌检测时，每个车牌映射为一个 `VehicleItem`（`track_id=0`，`vehicle_box=plate_box`，`vehicle_score=plate_score`，`vehicle_type="unknown"`，`plate_count=1`，`plates=[plate]`）。

### R3. Runner 进程 IPC 序列化与跨进程传输
- `zhulong_algo_runner` 的 `format_inference_json` 新增对 `ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE` 的处理，输出 `"task_type": "vehicle_structure"`，并序列化 `vehicles` 数组及其嵌套的 `plates` 数组。
- 补齐 Runner 单测断言。

### R4. Go 宿主统一感知模型与 IPC 解析
- `internal/aitypes` 新增常量 `TaskTypeVehicleStructure = "vehicle_structure"` 与 `ResultTypeVehicleStructure = 4`。
- 定义 `PlateSubItem` 与 `VehicleItem` Go 结构体。
- `InferenceResult` 增加 `Vehicles []VehicleItem` 字段。
- 确保 JSON 反序列化与契约测试全覆盖。

### R5. 算法包元数据与准入校验放行
- `manifest.json` 中的 `task_type` 支持 `"vehicle_structure"`。
- 后端 `internal/algorithm` 与前端 `features/algorithms` 放行 `"vehicle_structure"` 枚举与校验。

### R6. 抓拍持久化与去重落库
- `internal/record/service.go` 的 `ProcessInferenceResult` 增加对 `TaskTypeVehicleStructure` 的路由处理 `processVehicleStructure`。
- 将车辆与车牌信息存入 `captures` 表（`target_type = "vehicle"`），并在 `attributes_json` 中结构化持久化 `plate_number`、`plate_color`、`plate_type`、`vehicle_color`、`vehicle_type`。
- 复用基于 `track_id` 或车牌号/坐标的冷却时间去重器，防止同辆车逐帧刷库。

### R7. 前端实时流叠加与类型契约
- `web/src/features/live/types/ai-result.ts` 补充 `plateSubItemSchema` 与 `vehicleItemSchema`，`aiResultEventSchema` 支持 `vehicles` 字段。
- 前端视频画框服务支持机动车与车牌的渲染展示。

---

## Acceptance Criteria

- [x] `native/sdk/include/zhulong/plugin.h` 与 `algo.hpp` 增加 `zhulong_plate_item`、`zhulong_vehicle_item` 及静态断言，Native 测试套件通过；
- [x] Runner 能够正确加载 mock 车辆结构化插件并输出规范的 IPC JSON；
- [x] Go 宿主 `internal/aitypes` 正确解析并映射机动车推理结果，`-race` 测试通过；
- [x] `internal/record` 收到 `vehicle_structure` 事件后能正确完成冷却防抖并持久化至 `captures` 表；
- [x] 前端 TypeScript 与 Zod schema 校验通过，`pnpm test` 全绿；
- [x] 收工前 `make check`（包含前端 lint/test、native-test、go-check）全量通过。
