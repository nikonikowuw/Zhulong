# Implementation Plan: vehicle_structure 端到端工程实施

## Phase 1: Native C ABI 与 C++ SDK 层

- [x] **Step 1.1**: 在 `native/sdk/include/zhulong/plugin.h` 中扩展 `zhulong_result_type_t`，定义 `zhulong_plate_item` 与 `zhulong_vehicle_item`。
- [x] **Step 1.2**: 在 `native/sdk/include/zhulong/algo.hpp` 中新增面向对象的 C++ 结构体（`PlateItem`, `VehicleItem`），编写 `VehicleStructureResultAdapter`（单块连续 Arena 内存打包）及静态断言校验，增加 `ZHULONG_REGISTER_VEHICLE_STRUCTURE_PLUGIN` 宏。
- [x] **Step 1.3**: 在 `native/tests/` 补充 C ABI 对齐与 Mock 插件单测，运行 `make native-test` 验证。

## Phase 2: Runner IPC 进程间传输与序列化

- [x] **Step 2.1**: 在 `native/src/algorithm/runner/runner.cpp` 的 `format_inference_json` 中实现 `ZHULONG_RESULT_TYPE_VEHICLE_STRUCTURE` 的 JSON 格式化输出。
- [x] **Step 2.2**: 补充 Runner 对 `vehicle_structure` 的测试断言，验证 IPC 协议格式正确无误。

## Phase 3: Go 宿主 aitypes 契约与 record 抓拍落库

- [x] **Step 3.1**: 在 `internal/aitypes/model.go` 中添加 `TaskTypeVehicleStructure`、`PlateSubItem`、`VehicleItem` 与 `InferenceResult.Vehicles` 字段，更新反序列化逻辑并增加测试。
- [x] **Step 3.2**: 在 `internal/algorithm/result.go` 中暴露对应的类型别名。
- [x] **Step 3.3**: 在 `internal/record/service.go` 中实现 `processVehicleStructure`，完成机动车与车牌的冷却去重及存入 `captures` 表。
- [x] **Step 3.4**: 编写 `internal/record/capture_vehicle_test.go` 并执行 `go test -race ./internal/...`。

## Phase 4: 算法元数据放行与前端契约

- [x] **Step 4.1**: 在 `web/src/features/algorithms/data/schema.ts` 中将 `vehicle_structure` 加入算法任务类型枚举。
- [x] **Step 4.2**: 在 `web/src/features/live/types/ai-result.ts` 中定义 `plateSubItemSchema`、`vehicleItemSchema`，在 `aiResultEventSchema` 引入 `vehicles` 校验。
- [x] **Step 4.3**: 运行前端单元测试 `cd web && pnpm test` 确认通过。

## Phase 5: 全链路质量验证与收工

- [x] **Step 5.1**: 执行 `make check`（前端 lint/type-check/test + native-test + api-docs + go-check）。
- [ ] **Step 5.2**: 组装车牌识别算法插件模板（`packages/plate_recognition/`），完成交付验证。
