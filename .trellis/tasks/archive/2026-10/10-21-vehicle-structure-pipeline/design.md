# Technical Design: vehicle_structure 端到端架构设计

## 1. 架构原则

1. **对标 PersonStructure**：机动车结构化在设计理念、拓扑结构与内存生命周期上与 `person_structure` 完全一致，保持系统的对称性与一致性。
2. **纯 C ABI 物理隔离**：跨动态库边界禁止暴露 C++ class、虚表或 STL，所有结构体保证 8 字节严格自然对齐。
3. **单块连续 Arena 打包 (Contiguous Arena Packing)**：由插件适配器预估并一次性分配连续内存块，包含 `zhulong_vehicle_item` 数组与所有子 `zhulong_plate_item` 数组，通过 `internal_token` 记录并在 `result_release` 时单次释放，杜绝嵌套内存泄露。
4. **两级挂载与退化保全**：
   - 完备态：整车检测检出车身，车牌检测检出车牌并几何挂载（IoF / 包围盒包含）；
   - 退化态：仅跑车牌检测时，每个车牌自适应退化为 `VehicleItem`，车身框即车牌框，保障单车牌算法无障碍接入。

---

## 2. 数据结构设计与内存布局

### 2.1 C ABI 结构体契约

```c
typedef struct zhulong_plate_item {
    float   box[4];            /* 16 bytes: [x1, y1, x2, y2] 严格归一化 [0.0, 1.0] */
    float   score;             /*  4 bytes: 置信度 (0.0 ~ 1.0) */
    char    plate_number[32];  /* 32 bytes: 车牌号码 UTF-8 字符串 (末尾 '\0') */
    char    plate_color[16];   /* 16 bytes: 车牌颜色 (末尾 '\0') */
    int32_t plate_type;        /*  4 bytes: 车牌类型 */
    uint32_t reserved_;        /*  4 bytes: 显式填充对齐 */
} zhulong_plate_item;
/* 总计: 16 + 4 + 32 + 16 + 4 + 4 = 76 ?
   等待: 16 + 4 + 32 + 16 = 68. 68 + 4 (type) = 72. 72 % 8 == 0.
   因此: box(16) + score(4) + plate_number(32) + plate_color(16) + plate_type(4) = 72 字节! */

typedef struct zhulong_vehicle_item {
    int64_t  track_id;         /*  8 bytes: 车辆跟踪 ID (退化填 0) */
    float    vehicle_box[4];   /* 16 bytes: [x1, y1, x2, y2] 归一化 (退化同 plate_box) */
    float    vehicle_score;    /*  4 bytes: 车辆置信度 */
    char     vehicle_type[32]; /* 32 bytes: 车型 */
    char     vehicle_color[16];/* 16 bytes: 车身颜色 */
    uint32_t plate_count;      /*  4 bytes: 挂载车牌数 */
    uint32_t reserved_;        /*  4 bytes: 指针前置对齐填充 */
    const zhulong_plate_item* plates; /* 8 bytes: 指向同一 Arena 内部偏移的只读借用指针 */
} zhulong_vehicle_item;
/* 总计: 8 + 16 + 4 + 32 + 16 + 4 + 4 + 8 = 92?
   重算:
   track_id (8, offset 0)
   vehicle_box (16, offset 8)
   vehicle_score (4, offset 24)
   vehicle_type (32, offset 28) -> offset 60
   vehicle_color (16, offset 60) -> offset 76
   plate_count (4, offset 76) -> offset 80
   reserved_ (4, offset 80) -> offset 84 (对齐到 8 字节边界)
   plates (8, offset 88) -> offset 96!
   故总尺寸为 96 字节，满足 96 % 8 == 0！ */
```

### 2.2 Arena 连续内存打包算法
设共有 $N$ 辆机动车，第 $i$ 辆车挂载 $M_i$ 块车牌，总车牌数 $P = \sum M_i$。
- 主体数组所需空间：$Size_{veh} = N \times sizeof(zhulong\_vehicle\_item)$（已 8 字节对齐）；
- 车牌数组所需空间：$Size_{plate} = P \times sizeof(zhulong\_plate\_item)$；
- 申请总内存：$TotalSize = Size_{veh} + Size_{plate}$，单次 `std::malloc(TotalSize)`；
- 内存首部为 `zhulong_vehicle_item` 数组，尾随其后连续存放各个车牌数组；
- 每个 `vehicle.plates` 偏移指针直接指向 Arena 内部地址；
- `out.data = arena_base`，`out.internal_token = arena_base`；
- `release` 函数执行 `std::free(token)`，完成一次性 O(1) 析构。

---

## 3. 跨进程 IPC JSON 格式与 Go 结构映射

### 3.1 IPC 格式
```json
{
  "camera_id": "cam_01",
  "algorithm_id": "plate_rec_01",
  "pts": 123456,
  "cost_ms": 12,
  "task_type": "vehicle_structure",
  "detections": [],
  "classifications": [],
  "persons": [],
  "vehicles": [
    {
      "track_id": 0,
      "vehicle_box": [0.1, 0.2, 0.4, 0.5],
      "vehicle_score": 0.95,
      "vehicle_type": "car",
      "vehicle_color": "white",
      "plates": [
        {
          "box": [0.15, 0.35, 0.35, 0.45],
          "score": 0.98,
          "plate_number": "粤B88888",
          "plate_color": "蓝色",
          "plate_type": 0
        }
      ]
    }
  ]
}
```

### 3.2 Go 宿主持久化策略
在 `internal/record/service.go` 中：
- 当收到 `TaskTypeVehicleStructure`，遍历 `res.Vehicles`；
- 如果车辆或车牌通过冷却去重（优先按车牌号去重，若无车牌号则按 track_id 或坐标去重）；
- 构造 `Capture` 记录：
  - `TargetType = "vehicle"`
  - `Box = vehicle_box`
  - `Score = vehicle_score`
  - `Attributes = {"vehicle_type": ..., "vehicle_color": ..., "plate_number": ..., "plate_color": ...}`
- 执行写入 `captures` 表，保障事件中心和抓拍列表可以按车型/车牌查看。

---

## 4. 影响面评估与测试保障

| 模块 | 修改文件 | 风险与防御 |
| :--- | :--- | :--- |
| Native C ABI | `native/sdk/include/zhulong/plugin.h` | 结构体内存对齐与尺寸校验，`static_assert` 守护 |
| Native SDK | `native/sdk/include/zhulong/algo.hpp` | Arena 连续内存分配与单次释放，无内存泄漏 |
| Runner IPC | `native/src/algorithm/runner/runner.cpp` | JSON 格式化与空数组防护 |
| Native Tests | `native/tests/plugin_c_abi_test.c` 等 | C ABI 兼容性与符号导出测试 |
| Go Models | `internal/aitypes/model.go` | 单元测试与序列化反序列化覆盖 |
| Record Service | `internal/record/service.go` | 冷却去重与落库回归测试（-race） |
| Web UI | `web/src/features/live/types/ai-result.ts` | Zod schema 解析与类型断言回归测试 |
