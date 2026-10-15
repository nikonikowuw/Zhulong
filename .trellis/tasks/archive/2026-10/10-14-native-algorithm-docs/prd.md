# native 算法子系统注释补齐

> 父任务：`.trellis/tasks/10-14-native-core-code-docs/`
> ⚠️ 动手前必读父任务 `design.md` §3（风格基线）与 §4（术语表）。
> ⚠️ 本子任务**不单独编写 design.md / implement.md**：技术设计与执行计划由父任务统一持有。

## 1. 目标

为 `native/src/algorithm/{ipc,runner,runtime}/` 与 `native/src/backends/{rknn,rga}/` 补齐中文注释。

**这是三个子任务中风险点密度最高的一个**：IPC 的字节级对齐契约、SHM 槽位代次防 ABA、Arena 单块内存打包、Runner 退出码表、RKNN 的 SDK 缺陷规避、RGA 的驱动级禁令——这些约束一旦在代码现场不可见，后来者极易写出"看起来对、实际挂机器"的改动（例如把目标缓冲区指针偏移一下，就会让 RGA 驱动陷入 D 状态死锁并触发看门狗复位）。

⚠️ 本层注释的**每一个数字**（字节数、超时、阈值、对齐值）都必须与代码中的 `static_assert` / 常量**逐一对齐后才能写入**。宁可写"见代码常量"，也不得凭记忆填数。

---

## 2. 范围（28 文件 / ~2400 行）

### 2.1 IPC（`native/src/algorithm/ipc/`，8 文件）

| 文件 | 行数 | 当前注释率 |
| --- | --- | --- |
| `shm_layout.hpp` | 28 | 0% |
| `ipc_protocol.hpp` | 96 | 5% |
| `shm_pool.hpp` | 69 | 20% |
| `shm_pool.cpp` | 163 | 1% |
| `unix_socket.hpp` | 83 | 0% |
| `unix_socket.cpp` | 440 | 3% |
| `frame_broker.hpp` | 101 | 5% |
| `frame_broker.cpp` | 395 | 4% |

### 2.2 Runner（`native/src/algorithm/runner/`，2 文件）

| 文件 | 行数 | 当前注释率 |
| --- | --- | --- |
| `runner.cpp` | 494 | 4% |
| `json.hpp` | 37 | 0% |

### 2.3 Runtime（`native/src/algorithm/runtime/`，11 文件）

| 文件 | 行数 | 当前注释率 |
| --- | --- | --- |
| `tensor.hpp` | 84 | 0% |
| `inference_backend.hpp` | 39 | 7% |
| `vision_backend.hpp` | 37 | 8% |
| `runtime_factory.hpp` | 14 | 14% |
| `runtime_factory.cpp` | 32 | 0% |
| `cpu_inference_backend.hpp` | 30 | 3% |
| `cpu_inference_backend.cpp` | 57 | 5% |
| `cpu_vision_backend.hpp` | 18 | 0% |
| `cpu_vision_backend.cpp` | 160 | 0% |
| `letterbox_geometry.hpp` | 24 | 37% |
| `letterbox_geometry.cpp` | 33 | 9% |

### 2.4 Backends（`native/src/backends/`，7 文件）

| 文件 | 行数 | 当前注释率 |
| --- | --- | --- |
| `rknn/rknn_types.hpp` | 59 | 13% |
| `rknn/rknn_error_map.hpp` | 43 | 18% |
| `rknn/rknn_inference_backend.hpp` | 22 | 18% |
| `rknn/rknn_inference_backend.cpp` | 262 | 8% |
| `rga/rga_error_map.hpp` | 48 | 35% |
| `rga/rga_vision_backend.hpp` | 115 | 35% |
| `rga/rga_vision_backend.cpp` | 525 | 7% |

### 明确排除

- `native/vendor/**`（**含 `rknpu2/` 与 `rga/` 的厂商头文件，绝不动**）。
- `native/tests/**`（含 `tests/plugins/mock_*`，本任务不补）。
- 公开 SDK 头文件（由 `10-14-native-public-contract-docs` 负责）。
- 行为变更、重构。

### ⚠️ 并发冲突提示（执行前必读）

本子任务范围中的 **3 个文件**正被 `10-14-inference-result-egress`（`in_progress`）修改，且是真实功能代码（`MSG_RESULT` 消息类型、`FrameBroker::set_on_result`、`Runner::serialize_inference_result`）：

```txt
native/src/algorithm/ipc/ipc_protocol.hpp
native/src/algorithm/ipc/frame_broker.hpp
native/src/algorithm/runner/runner.cpp
```

**建议顺序：先等 `10-14-inference-result-egress` 提交，再开始本子任务。**

若必须并行，则跳过上述 3 个文件，只做其余 25 个，并在任务 `implement.md` 中登记待补项。⚠️ 绝不可在这些文件上叠加注释后再让其继续编辑 —— 会导致 diff 交错，`git add -p` 难以切分。

相关执行细节见父任务 `implement.md` §1（前置条件）与 §7.3。

---

## 3. 重点内容清单

### 3.1 IPC 协议与对齐（R6、R7）

- ⚠️ **8 字节自然对齐**：IPC 二进制包中 64 位字段（`generation` / `stream_id` / `frame_id` / `pts`）必须排布在 8 字节自然对齐偏移；结构体总尺寸为 8 的倍数并含尾部填充。说明 `static_assert(offsetof(...) % 8 == 0)` 与总大小校验的**意图**（ARM64/嵌入式非对齐访问）。
- ⚠️ **四态 socket 状态机**（`SUCCESS` / `IDLE_TIMEOUT` / `CLOSED` / `DESYNC_ERROR`）与**半包熔断**：已收到部分字节后超时或出错必须标记 `DESYNC_ERROR` 并主动断开，禁止在错位字节流上继续读。
- ⚠️ **64KB payload 门禁**：`MAX_IPC_PAYLOAD_SIZE` 与 Go 侧 `MaxIpcPayloadSize` 必须一致（由 `internal/algorithm/ipc_contract_test.go` 跨语言比对），注释需点明这一跨语言耦合。
- **50ms `SO_SNDTIMEO`**：发送超时的意图（防止卡死子进程反向阻塞主媒体流水线）。

### 3.2 SHM 与 FrameBroker（R8、R9）

- 槽位状态机 `FREE → WRITING → LEASED → FREE`。
- ⚠️ **代次（generation）防 ABA**：客户端归还租约时必须回传对应代次；代次不匹配的 ACK 必须**静默丢弃**，绝不影响当前租约。
- ⚠️ **慢消费者隔离**：`in_flight_count` 上限（默认 2）达到时对该客户端**丢弃最新帧**，绝不阻塞主视频流或其他算法包。
- ⚠️ **崩溃自愈**：检测到 socket 断开或 `POLLHUP` 时立即扫描回收该客户端在所有槽位上的未归还租约。
- **在途配额原子回滚**：`publish_frame` 失败时必须回滚 `in_flight_count` / `delivered_frames` 并计入 `dropped_frames`。
- ⚠️ **多平面逐行安全复制**：必须依据 `host_planes[4]` / `plane_strides[4]` 分平面逐行拷贝，严禁把 `data[0]` 当作单一连续缓冲越界 `memcpy`。
- **只读映射**：Runner 侧以 `PROT_READ` 映射，物理防止算法进程篡改原始画面。
- `ShmHeader` 的 `magic` / `version` 校验与 `static_assert(sizeof(ShmHeader) == 64)` 的意图。

### 3.3 Runner（R1 相关）

- ⚠️ **退出码表**（0 正常 / 1 `dlopen` 失败或 client name 超长 / 2 `model_create` 失败 / 3 `worker_create` 失败 / 4 `task_open` 失败 / 5 broker 连接失败或无法反解 stream_id），以及**为什么运维与 Go 看门狗依赖这张表**。
- ⚠️ **`--broker` 字面路径语义**与 `parse_stream_id_from_socket` 的约定：runner 从 `<base_dir>/zhulong_broker_<stream_id>.sock` **反解** `stream_id` 作为 `task_id`（IPC 握手协议未承载 stream_id）。注释必须点明这是**唯一**允许依赖路径命名格式的地方。
- ⚠️ **多路 `--broker` 的 D6 语义**：任一路失败即置位 `stop_all`，**已连上的路也被一并终止**，不存在"部分连接成功"的中间态。
- `json.hpp` 的字符串转义要求（含 U+0020 以下控制字节）。

### 3.4 Runtime HAL

- `IInferenceBackend` / `IVisionBackend` 的抽象边界与"后端可替换"前提（`letterbox_geometry.hpp` 已说明几何必须共用一套语义，注释需与之呼应）。
- `Tensor`：形状/元素数/字节数溢出检查的意图（`std::invalid_argument` vs `std::length_error`）；空 Tensor 的 `element_count() == 0`；⚠️ 动态 shape 不在当前 API 支持范围。
- `runtime_factory`：**编译期**后端选择（非运行期 `dlopen`），以及 `algo.hpp` 直接依赖 runtime 私有头带来的后果（`RknnInferenceBackend` 必须编进 `libZhulongAlgoRuntime.so`）。
- `CpuVisionBackend`：stride 契约（`strides[0] == 0` 表示 packed；正 stride 至少 `width * 3`；负值或不足返回 `INVALID_ARGUMENT`）；未实现格式（NV12 / YUV420P / RGBA32 / UNKNOWN）返回 `NOT_SUPPORTED` 且**不得读取源像素**；Letterbox 补边值 114；像素中心坐标双线性插值。

### 3.5 RKNN 后端（R10~R14）

- ⚠️ **R10（最重要）**：主 root 上下文**绝不**调用 `RKNN_QUERY_MEM_SIZE` —— 该查询会污染上下文使首次 `rknn_dup_context` SIGSEGV。需要该数据时用独立的一次性上下文查询后立即销毁。注释需写明"这是 2.3.2 实测缺陷，官方性质未定"。
- ⚠️ **R11**：`rknn_dup_context` 派生必须经 `dup_mutex` 串行化；失败路径不得 double-destroy（`root_valid` 置位时机）。
- ⚠️ **R13**：`rknn_input` **无 stride 字段** → 传入 buffer 必须 packed（padded 会**静默算错**而非报错）；输出经 `want_float=1` 统一转 float32 后**拷贝**进 `Tensor`。⚠️ **不得声称零拷贝**。
- ⚠️ **R14**：链接目标必须是 `librknnrt.so`；`librknn_api.so` 是陈旧残留（1.5.2，25 个符号），严禁链接。此条属构建期约束，注释写在 `rknn_inference_backend.hpp` 的文件头块中。
- 权重共享的实测收益（约 3.5×）与"逻辑内存值不能当物理占用"的说明。
- ⚠️ 单 NPU 核：不得声称吞吐随 worker 数线性扩展；不启用 `RKNN_FLAG_ASYNC_MASK`。
- 错误映射口径：`DEVICE_UNAVAILABLE` 等映射为 `NOT_SUPPORTED`（环境问题）而非 `INTERNAL`（代码 bug）—— 说明这一区分对上层运维的意义。
- ⚠️ `rknn_api.h` 只允许出现在 `backends/rknn/`；注释中不得把该头文件的类型泄露到其他层。

### 3.6 RGA 后端（R12）

- ⚠️ **R12（最重要）**：目标缓冲区指针**绝不偏移** —— 内容区偏移必须由 `improcess` 的 `drect` 表达。偏移指针会让驱动按完整 `wstride × hstride` 校验，访问超出 Tensor 边界（实测约 242KB 越界），引发 RGA 陷入 D 状态死锁并触发看门狗复位。注释必须写明**症状**，而不只是禁令。
- ⚠️ **强依赖 `imcheck_t` 预校验**：每次 `improcess` 前校验 `srect` / `drect`，失败即返回并在上层回退。
- **两阶段裁剪（`crop_scale`）**：ROI 坐标不满足 2 像素对齐时采用"形式 1（改参数向外扩取整）"，避免 O(N) 内存搬移。
- **运行期硬件探测**：`byte_stride_align` 从 `/sys/kernel/debug/rkrga/hardware` 读取（RK3568 实测 4），`offset_align` 通过 `imcheck_t` 动态探测（实测 2）。⚠️ **严禁硬编码**，读取失败透明回退 CPU。
- **格式能力矩阵**：NV12 / YUV420P 仅支持连续内存，非连续内存安全回退 CPU 返回 `NOT_SUPPORTED`。
- **颜色转换语义**：硬件 CSC 为 Limited-range BT.601。
- ⚠️ **不得声称零拷贝**：当前走 `wrapbuffer_virtualaddr` 虚拟地址映射，存在用户态到 DMA 映射开销。
- 滤波差异：与 CPU 软解的核函数不同，下采样时单像素极差可达 119，测试容差 `worst <= 128`。
- **设备权限**：`/dev/rga` 为 `root:video`，运行用户须加入 `video` 组。

---

## 4. 验收标准

### 4.1 继承父任务共性标准

- [x] **AC-D1（覆盖率）**：范围内每个公开声明与每个**非平凡逻辑块**（协议打包、状态机、内存租赁、错误映射、厂商约束）都有注释。
- [x] **AC-D2（零代码变更）**：父任务 `implement.md` §3 的注释剥离不变性检查，全量输出 `OK`。
- [x] **AC-D3（构建测试）**：`make native-build` 成功；`make native-test` 全绿（期望 15/15）。
- [x] **AC-D4（注释质量）**：无逐行复述；无臆造技术细节。
- [x] **AC-D5（既有注释保真）**：既有注释未被删除或改变技术含义。

### 4.2 本子任务专属

- [x] **AC-C3**：§3 重点内容清单逐项落地。
- [x] **AC-C3-a**：父任务 `prd.md` §4.3 中归属 C3 的风险点 **R1、R2、R6、R7、R8、R9、R10、R11、R12、R13、R14** 全部在注释中有明确落点。
- [x] **AC-C3-b**：注释中出现的**每一个数字常量**都与代码中的 `static_assert` / 常量定义逐一核对过，且与 `.trellis/spec/native/algo-plugin-contract.md` §6/§8/§9 一致。
- [x] **AC-C3-c**：注释中未出现父任务 `design.md` §4 术语表列出的禁用别名（`Arena` 统一译"单块连续内存"、`lease` 译"租约"、`generation` 译"代次"、`in-flight` 译"在途"、`operator` 译"算子"）。
- [x] **AC-C3-d**：⚠️ RKNN 与 RGA 相关注释**明确写出"不得声称零拷贝"**，如实描述当前存在的拷贝（spec §8.9 / §9.6 的硬性要求）。
- [x] **AC-C3-e**：⚠️ `rknn_api.h` / `rga/im2d.h` 的厂商类型**未出现在** `backends/` 之外的任何注释中（`directory-structure.md` 铁律 4 的延伸）。

---

## 5. 参考

| 用途 | 路径 |
| --- | --- |
| 风格基线 / 术语表 | 父任务 `design.md` §3 / §4 |
| 执行循环 / 门禁 / 回滚点 | 父任务 `implement.md` §2 / §4 / §5 |
| **本层权威来源（必读）** | `.trellis/spec/native/algo-plugin-contract.md` §2、§5、§6、§8、§9 |
| 目录隔离铁律 | `.trellis/spec/native/directory-structure.md` |
| CGO 边界 | `.trellis/spec/native/cgo-contract.md` |
| 既有风格样本 | `native/src/algorithm/ipc/shm_pool.hpp`、`native/src/algorithm/ipc/frame_broker.hpp`、`native/src/algorithm/runtime/letterbox_geometry.hpp` |
