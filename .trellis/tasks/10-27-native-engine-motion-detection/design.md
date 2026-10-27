# Native Engine Motion Detection Pipeline 技术设计

## 1. 架构全景与数据流 (Architecture & Data Flow)

```txt
┌────────────────────────────────────────────────────────────────────────┐
│ C++ Native Engine Pipeline                                             │
│                                                                        │
│  HardwareFrame (NV12 / YUV420P)                                        │
│         │                                                              │
│         ▼                                                              │
│  [ MotionDetector ] ── 320×180 步长采样 Y 通道 (57.6KB, L2 Cache 亲和)   │
│         │           ── 绝对帧差 + 阈值二值化 (diff > 25)               │
│         │           ── 全屏光照跳变保护 (>70% 变化则重置底图)           │
│         │           ── 块聚类提取 Top-4 外接框 [x1, y1, x2, y2]         │
│         │           ── 运动停止后 hold-off 继续放行 N 帧              │
│         ▼                                                              │
│    MotionResult { has_motion, box_count, motion_boxes[4] }             │
│         │                                                              │
│         ▼                                                              │
│  [ FrameBroker::publish_frame ] ── 将运动元数据写入 FrameDispatchMsg    │
└─────────────────────────────────┬──────────────────────────────────────┘
                                  │ IPC Unix Domain Socket (96B -> 168B)
                                  ▼
┌────────────────────────────────────────────────────────────────────────┐
│ 统一 Runner 子进程骨架 (algo_main.cpp / libzhulong_algo_sdk.a)          │
│                                                                        │
│  解析 FrameDispatchMsg:                                                │
│  // 遗留 96B 报文 → 运动字段取默认值（has_motion = 1）全帧推理         │
│  if (gate::should_skip_inference(dispatch.has_motion)) {               │
│      // 画面完全静止（含 hold-off 已结束）                              │
│      // ⚠️ 此处不做 DMA-BUF mmap/SYNC，只发消失信标 + ACK              │
│      goto ack_and_skip;                                                │
│  }                                                                     │
│  // 【R4b·未实现】ROI 几何预门禁：需要区域配置下发通道，见 PRD R4b        │
│                                                                        │
│  // 触发推理:                                                          │
│  api.process(task, &view, &result);                                    │
│                                                                        │
│ ack_and_skip:                                                          │
│  send_msg(MSG_FRAME_ACK);                                              │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 核心模块与算法设计

### 2.1 MotionDetector 核心实现 (`native/src/pipeline/motion_detector.hpp`)

- **固定网格分辨率**：$W_{grid} = 320, H_{grid} = 180$（总计 57,600 字节），预分配两个 `std::vector<uint8_t>` 作为前后帧缓冲区，运行期零堆分配；聚类队列与连通域结果都是**栈上定长数组**（`BLOCK_COUNT = 144`），不碰 `std::vector` / `std::queue`。
- **图像像素采样**：
  - 针对 NV12 / YUV420P，Y 平面首地址天然为灰度图；
  - 步长映射：$x_{src} = x \cdot W_{src} / W_{grid}, \quad y_{src} = y \cdot H_{src} / H_{grid}$；
  - 从 `frame.host_planes[0]` 或 `frame.host_ptr` 读取；若为 DMA-BUF（硬解帧），通过 `mmap` 与 `DMA_BUF_IOCTL_SYNC` 建立只读映射。
  - ⚠️ **代价诚实描述**：检测本身零堆分配、零系统调用，但 MPP 硬解帧没有 host 映射，因此每帧要做 `mmap` + `IOCTL_SYNC(START/END)` + `munmap` 三次系统调用。这是「不额外持有解码缓冲」的取舍，不要写成纯缓存命中；性能数字必须先实测再写进文档。
- **运动判定状态机**：
  1. 首帧输入：填充 `prev_grid_`，返回 `has_motion = false, box_count = 0`（初始化）。
  2. 帧差计算：$D(x, y) = |I_{curr}(x, y) - I_{prev}(x, y)|$。
  3. 阈值化：$D(x, y) > 25$ 计为一个活跃运动像素。
  4. 光照跳变抑制（Anti-flash / Scene change）：
     若活跃像素比例超过 70%（$> 40,320$ 个像素），判定为环境光突变（如红外夜视切换或相机自动曝光调整），立即用当前帧覆盖 `prev_grid_`，并返回 `has_motion = false`，避免瞬间全屏误报。
  5. 最小运动门槛：
     若活跃像素数 $< 30$（$< 0.05\%$），视为传感器微噪，返回 `has_motion = false`。
  6. 网格聚类（Bounding Box Extraction）：
     将 $320 \times 180$ 划分为 $16 \times 9$ 个宏块（每块 $20 \times 20$ 像素）。统计有活跃像素的宏块，利用 8-连通域 BFS 聚合为最多 4 个外接矩形，按宏块数降序取前 4，归一化到 $[0.0, 1.0]$。
  7. 运动保持（hold-off）：
     判定为静止时，若离上一次运动不足 `MOTION_HOLD_FRAMES`（默认 10 帧，@5fps ≈ 2s），仍返回 `has_motion = true` 但 `box_count = 0`（无外接框 ⇒ 调用方按全帧处理）。理由：逐帧差分是瞬时判据，缺了它「主体站住不动」会立刻被判成目标消失，引发结果抖动与误清空。

---

### 2.2 IPC 协议扩展 (`native/src/algorithm/ipc/ipc_protocol.hpp`)

在 `FrameDispatchMsg` 尾部追加运动字段：

```cpp
constexpr size_t MAX_IPC_MOTION_BOXES = 4;

struct MotionBox {
    float x1{0.0f};
    float y1{0.0f};
    float x2{0.0f};
    float y2{0.0f};
};

struct FrameDispatchMsg {
    // 现有 96 字节字段（stream_id, frame_id, generation, pts, capture_time_ms,
    // slot_index, width, height, format, strides[4], offsets[4], reserved_fd, reserved_pad）
    
    // --- 运动检测扩展字段 (72 字节) ---
    uint32_t has_motion{1};       /**< 1 表示有运动或未启用检测（默认放行）；0 表示画面静止 */
    uint32_t motion_box_count{0}; /**< 有效运动外接框数量（0 ~ MAX_IPC_MOTION_BOXES） */
    MotionBox motion_boxes[MAX_IPC_MOTION_BOXES]; /**< 4 * 16 = 64 字节归一化坐标 */
};

static_assert(sizeof(MotionBox) == 16, "MotionBox size mismatch");
static_assert(sizeof(FrameDispatchMsg) == 168, "FrameDispatchMsg size mismatch");
static_assert(offsetof(FrameDispatchMsg, has_motion) % 8 == 0, "has_motion unaligned");
static_assert(offsetof(FrameDispatchMsg, motion_boxes) % 8 == 0, "motion_boxes unaligned");
```

- **严格 8 字节自然对齐保证**：
  $96 + 4 + 4 + 64 = 168$ 字节，$168 \pmod 8 == 0$。
  所有 64 位整数字段和浮点数组偏移严格被 8 整除，完全规避 ARM64 非对齐访问惩罚。

---

### 2.3 Runner 骨架自动拦截 (`native/sdk/src/algo_main.cpp`)

在 `algo_main.cpp` 中收到 `MSG_FRAME_DISPATCH` 后：
0. **报文解码（前缀兼容）**：统一走 `gate::decode_frame_dispatch`。旧宿主的 96 字节报文按遗留前缀解析，运动字段取默认值（`has_motion = 1`）⇒ 全帧推理。
1. **静止拦截**：若 `gate::should_skip_inference(has_motion)` 成立（只有 `has_motion == 0` 才算），记录静止计数，直接跳过 `api.process`，且**不做** DMA-BUF 映射。
2. **ROI 几何相交拦截（R4b，未实现）**：留给独立特性；`motion_boxes` 已在线上但当前无消费者，ROI/MASK 仍由宿主侧 `internal/region.Filter` 在结果侧生效。
3. **租约归还保证**：
   - 跳过推理时，必须严格执行清理与 ACK：
     释放 DMA 描述符与映射（若已有），构造 `FrameAckMsg` 回传 Broker，保证共享内存槽位正常流转，永不泄漏。

---

## 3. 兼容性与安全设计

1. **Fail-Open 降级设计**：
   - 若 Native 运动检测因格式不支持或内存映射失败，`has_motion` 置为 `1`，`motion_box_count = 0`，自动退化为全帧正常推理，保证业务永不漏检。
2. **去噪与心跳兼容**：
   - 跳过推理时，不需要上报空结果 JSON（除非上一帧有目标且需要发送清空信标 `last_had_targets`）。
3. **现存算法包兼容性**：
   - 算法插件仅通过 `api.process` 暴露，完全不感知 `algo_main.cpp` 内部的拦截逻辑。
4. **版本偏斜矩阵**：
   - 旧宿主 + 新 Runner：按 96 字节前缀解析，全帧推理（不漏检，只是不省算力）。
   - 新宿主 + 旧 Runner：多出的 72 字节被忽略，旧 Runner 照常推理。
   - 两种偏斜都不会出现「既不推理也不 ACK」的静默退化。

---

## 4. 评审修正（本次实现相对初稿的差异）

| 项 | 初稿 | 现状 | 理由 |
| --- | --- | --- | --- |
| 光照跳变阈值 | 65% | **70%**（与 PRD R1 一致） | 三份文档不能各说各话 |
| 运动停止语义 | 无 | **hold-off 10 帧** | 瞬时差分会让静止主体被判消失 |
| ROI 预门禁 | 列为已实现 | **显式降为 R4b（未实现）** | 无区域配置下发通道；判错会静默丢真目标 |
| 检测路径开销 | 「零堆分配 + 纯缓存命中」 | **检测核心零堆分配；DMA 路径每帧 3 次系统调用** | 文档不能写未测量的数字 |
| 报文长度门禁 | `>= sizeof(FrameDispatchMsg)` | **前缀兼容解码** | 新旧宿主/包错位不能静默退化 |
