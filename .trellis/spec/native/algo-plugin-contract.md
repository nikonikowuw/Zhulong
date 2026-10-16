# 算法插件系统契约与 SDK 规范 (Algorithm Plugin Architecture)

> 纯 C ABI 隔离、现代 C++17 SDK 门面、公共算法运行库 HAL 与通用 Runner。

---

## 1. 架构原则与边界约束

1. **一包一进程 (One Package, One Process)**：每个算法包运行在独立的 Native Runner 子进程中，故障隔离，崩溃不拖垮主媒体与 Go 服务。
2. **纯 C ABI 物理隔离**：插件动态库必须导出纯 C ABI（`zhulong_plugin_api_v1`），禁止跨 `.so` 边界暴露 C++ class、虚表、STL 或厂商专有 SDK 上下文。
3. **C++17 Header-only SDK 门面**：第三方开发者统一引入 `zhulong/algo.hpp`，通过 RAII、智能指针与操作符链式调用编写现代 C++17 算法，避免手动管理底层句柄。
4. **两层硬件抽象层 (HAL)**：公共运行库 `libzhulong_algo_runtime.so` 封装 `IInferenceBackend` 与 `IVisionBackend`，屏蔽不同加速器（RKNN、CANN、TensorRT、CPU）的差异。
5. **三层资源解耦**：
   - **Model Group**：全局只读模型权重，多线程共享；
   - **Worker Context**：线程绑定的执行会话与工作区内存，多线程并发互不干扰；
   - **Task State**：单路摄像机时序状态（跟踪器、平滑器），通过 task ID 独立存取。

---

## 2. 纯 C ABI 规范 (`native/sdk/include/zhulong/plugin.h`)

所有插件通过导出的 `zhulong_plugin_get_api` 填充 `zhulong_plugin_api_v1`。v1 API 布局包含 `struct_size`、`api_version`、插件元信息和模型/Worker/Task/process/result-release 回调。
支持三种结果载荷形态：

1. **检测**：`ZHULONG_RESULT_TYPE_DETECTION` (1) + `zhulong_bbox[]`（`box[4]` 严格归一化 `[x1, y1, x2, y2] ∈ [0.0, 1.0]`，总尺寸 88 字节，8 字节自然对齐）；
2. **分类**：`ZHULONG_RESULT_TYPE_CLASSIFICATION` (2) + `zhulong_classification_item[]`；
3. **结构化人体/人脸**：`ZHULONG_RESULT_TYPE_PERSON_STRUCTURE` (3) + `zhulong_person_item[]`（支持 1:N 嵌套人脸 `zhulong_face_item` 与 Top-K 候选人 `zhulong_face_candidate`）。
   - **方案 B 底库对账契约**：`zhulong_face_candidate` 包含 `char face_id[ZHULONG_FACE_ID_MAX_LEN]`（200 字节，8 字节整除自然对齐），精确记录候选人对应的单张登记照唯一标识；`zhulong_face_item` 包含 `char matched_face_id[ZHULONG_FACE_ID_MAX_LEN]`（280 字节，8 字节整除自然对齐），记录 Top-1 胜出底照 ID，支持宿主直接进行 1:1 双图（抓拍人脸 vs 底库登记照）核验。

全系统（通用目标检测、人体/人脸结构化、IPC 消息、Go 宿主领域模型与前端渲染）统一采用 `[x1, y1, x2, y2] ∈ [0.0, 1.0]` 严格归一化两点坐标，全面废弃 `(x, y, w, h)` 语义。
结果内存由插件创建，并只能通过同一插件的 `result_release` 释放。添加分类与结构化能力保持 v1 函数表布局稳定（数据层多态）。
`zhulong_frame_view` 包含 `int32_t dma_buf_fd` 字段（硬件 DMA-BUF 句柄，有效时 `>= 0`；POSIX SHM 虚拟内存降级时为 `-1`），利用原 4 字节自然填充缝隙安置，严格维持 8 字节自然对齐与 88 字节总尺寸不变。C++ SDK `zhulong::Image` 提供 `int dma_buf_fd() const noexcept` 便捷访问器。
结构化结果采用**单块连续 Arena 内存打包（Contiguous Arena Packing）**，所有嵌套子指针指向 Arena 内部偏移，`result->internal_token` 指向首地址，`result_release` 通过单次 `std::free` 彻底释放整棵树，杜绝内存碎片与嵌套泄漏。
结构化坐标严格归一化到 `[0.0, 1.0]`，关键点（5 点）同样严格归一化。纯人脸检测场景下采用退化语义：每个检出人脸映射为一个 `zhulong_person_item`（`track_id=0`，`body_box=face_box`，`body_score=1.0`）。

插件入口声明使用 `ZHULONG_PLUGIN_EXPORT`。启用 hidden default visibility 时，只显式导出 `zhulong_plugin_get_api`；Linux 示例插件还使用 GNU ld version script 将其他定义符号设为 local（仅靠编译器 visibility 可能在 sanitizer 构建暴露弱 RTTI 符号）。CMake 示例插件目标定义 `ZHULONG_PLUGIN_BUILD` 并设置 hidden C++ / inline visibility。

### 异常防爆门守则

导出入口和 API callbacks 必须以 `try { ... } catch (...)` 阻止 C++ 异常穿越动态库边界；失败返回 `ZHULONG_PLUGIN_ERR_INTERNAL`，并在失败时将输出句柄/result 初始化为空。

---

## 3. 现代 C++17 SDK 门面 (`native/sdk/include/zhulong/algo.hpp`)

算法类实现 `init(config_json)`、`create_session(worker_id)`，再按任务类型提供对应推理方法：

```cpp
class MyDetector {
public:
    zhulong_status_t init(const std::string& config_json);
    zhulong::Session create_session(uint32_t worker_id);
    std::vector<zhulong::Detection> detect(zhulong::Session&, const zhulong::Image&);
};
ZHULONG_REGISTER_DETECTOR_PLUGIN(MyDetector, "MyDetector", "1.0.0")

class MyClassifier {
public:
    zhulong_status_t init(const std::string& config_json);
    zhulong::Session create_session(uint32_t worker_id);
    std::vector<zhulong::Classification> classify(zhulong::Session&, const zhulong::Image&);
};
ZHULONG_REGISTER_CLASSIFIER_PLUGIN(MyClassifier, "MyClassifier", "1.0.0")

class MyPersonStructurePlugin {
public:
    zhulong_status_t init(const std::string& config_json);
    zhulong::Session create_session(uint32_t worker_id);
    std::vector<zhulong::PersonItem> detect_persons(zhulong::Session&, const zhulong::Image&);
};
ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(MyPersonStructurePlugin, "MyPersonStructure", "1.0.0")
```

Detector 宏调用 `detect()` 并序列化 bbox；Classifier 宏调用 `classify()` 并序列化 classification item；PersonStructure 宏调用 `detect_persons()` 并将多级嵌套树打包为单块连续 Arena 内存。三者共享 opaque handle lifecycle 和 exception boundary，但使用各自的 result type 与 payload。

### 3.1 多模型级联与 Session 协同机制 (Multi-Model Session Pairing)

当算法包需要组合多个模型（如「人脸检测 det_500m + 特征提取 facelivtv2-l」双模型流水线）时：

- 插件内部持有多个 `zhulong::Model` 实例（如 `det_model_` 与 `rec_model_`）；
- `zhulong::Session` 暴露 `uint32_t worker_id() const noexcept` 访问器；
- 在 `create_session(worker_id)` 中为各模型分别创建对应 `worker_id` 的会话，由插件容器维护映射（如 `rec_sessions_[worker_id]`）；
- 主入口返回检测会话作为主句柄；在 `detect_persons` 推理热路径中通过 `session.worker_id()` 索引对应的级联识别会话，保障多 Worker 并发安全；
- 仿射对齐推荐采用单阶段直写模式（如 `Aligner::crop_and_align_direct` 基于 Umeyama 逆矩阵直接将原帧采样写入特征输入 Tensor），消除中间裁剪过渡堆内存分配。

---

## 4. CPU 图像预处理与固定尺寸 Tensor 合同

`CpuVisionBackend` 仅支持 packed `RGB24` / `BGR24` 输入和目标格式，输出 Tensor 为 HWC、3 通道、`UINT8`。`resize` 的内部运行时签名为：

```cpp
zhulong_status_t resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                        zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                        int dst_w, int dst_h, zhulong_pixel_format_t dst_format);
```

- `zhulong_frame_view.strides[0] == 0` 表示 packed 紧密排列；正 stride 必须至少为 `width * 3`；负值或不足行宽返回 `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT`。
- `NV12`、`YUV420P`、`RGBA32`、`UNKNOWN` 等尚未实现的格式返回 `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED`，且不得读取源像素。
- RGB/BGR 目标顺序不一致时必须交换 R/B 通道。缩放采用像素中心坐标、双线性插值和边界 clamp；Letterbox 补边值为 114，缩放直接写到最终 Tensor ROI，不分配 `resized_temp`。
- 空默认 Tensor 的 `element_count()` 为 0；非空 shape 中任一维度 `<= 0` 抛出 `std::invalid_argument`；元素数或字节数乘法溢出抛出 `std::length_error`。动态 shape 分配不在当前 API 支持范围内。

### 校验矩阵与错误码

| 条件 | 结果 |
| --- | --- |
| 输入和目标格式均为 RGB24/BGR24，stride 为 0 或至少 `width * 3` | 成功 |
| 输入/目标格式包含尚未实现的格式 | `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED` |
| 空像素指针、非正图像尺寸、负 stride、stride 小于行字节数 | `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT` |
| 非空 Tensor shape 含零/负维度 | 抛出 `std::invalid_argument` |
| Tensor 元素数/字节数无法由 `size_t` 表示 | 抛出 `std::length_error` |

---

## 5. 独立黑盒验包 CLI 工具 (`zhulong_algo_runner`)

验包命令：

```bash
zhulong_algo_runner --verify-plugin <path_to_plugin.so> --sample <test_image_or_mock>
```

输出严格 JSON 行。所有字符串字段均转义；退出码 `0` 表示 ABI、模型加载、Worker 创建、Task 打开、单次推理与内存释放全部符合规范。

### 5.1 Runner 命令契约：`--broker` 参数语义

`zhulong_algo_runner --plugin <plugin.so> --broker <socket_path> [--broker <socket_path_2> ...] [--name <client_name>] [--max-frames <N>]`

- **`--broker` 的语义为字面 socket 路径**：客户端直接执行 `connect(path)`，不做目录派生。支持重复传入多个 `--broker` 参数以订阅多路相机流。
- **Native Engine 作为 Broker 宿主**：当流具有 `ConsumerAI` 消费者时，Native `Engine` 自动实例化并启动 `ipc::FrameBroker`，生成的 socket 路径为 `<base_dir>/zhulong_broker_<stream_id>.sock`。
- **`ZHULONG_FRAME_BROKER_SOCK` 环境变量**：仅在 Native 端作为 `base_dir` 读取（默认 `/tmp`），Go 侧不再读取该变量。
- **路径解析与传递**：Go 侧通过 C ABI `Zhulong_stream_get_broker_path`（Go 封装为 `Stream.BrokerPath(ctx)`）获取各相机流的实际 socket 路径，并经 `BrokerSubscriptionResolver` 组装为 `--broker <socket_path>` 列表传给 runner。
- **runner 从 socket 路径反解 stream_id**：runner 按 `<base_dir>/zhulong_broker_<stream_id>.sock` 从**字面 `--broker` 路径**反解 stream_id 并用作 `task_id`（`runner.cpp` 的 `parse_stream_id_from_socket`）。因此 `--broker` 传入的路径必须遵循宿主的派生规则。

### 5.2 Runner 退出码表

| 退出码 | 含义 | 出处 |
| :---: | :--- | :--- |
| `0` | 正常退出 | `runner.cpp` |
| `1` | `dlopen` 插件动态库失败 / 客户端名称超长（超过 32 字节） | `runner.cpp` |
| `2` | `model_create` 失败 | `runner.cpp` |
| `3` | `worker_create` 失败 | `runner.cpp` |
| `4` | `task_open` 失败 | `runner.cpp` |
| `5` | **broker 连接失败 / 无法从 `--broker` 路径反解 stream_id / 未提供 `--broker`** | `runner.cpp` |

⚠️ 多路 `--broker` 下，任一路连接失败即整体失败，退出码严格保持为 `5`；任一路失败会置位 `stop_all` 终止已连接各路，不存在部分成功中间态。

### 5.3 FrameBroker 宿主与帧发布通路

- 原生 `Engine` 托管 `FrameBroker` 与 `FramePublisher` 线程，当 `ConsumerAI` 挂载时拉起、卸载时销毁；
- 解码后的帧在 Native 侧由 `FramePublisher` 线程基于 `max(sample_fps)` 进行发布侧节流并推入共享内存；
- 零像素数据进入 Go GC 内存，Go 仅负责控制流与 socket 路径装配。

---

## 6. 跨进程 FrameBroker 与共享内存租赁契约 (FrameBroker & SHM Leasing)

### 6.1 内存拓扑与安全隔离

- 主进程作为生产者独占创建 POSIX 共享内存（`/zhulong_shm_<stream_id>`，带固定 8~16 个 4MB 槽位），并将解码帧写入空闲槽位；
- 各独立算法 Runner 子进程以只读权限（`PROT_READ`）映射共享内存，物理防止算法进程篡改原始画面；
- 主进程与各 Runner 之间通过点对点 Unix Domain Socket 交换定长二进制 IPC 控制包（`MSG_HANDSHAKE`, `MSG_FRAME_DISPATCH`, `MSG_FRAME_ACK`）。

### 6.2 租约生命周期与代次保护 (Generation)

- 每个槽位（Slot）维护单调递增的 64 位代次（`generation`）与活跃租赁集合（`active_leases`）；
- 槽位状态机严格遵循：`FREE` ➔ `WRITING` ➔ `LEASED` ➔ `FREE`；
- **防 ABA 悬挂确认**：客户端归还租约时必须回传对应帧的 `generation`；若代次不匹配（如槽位已超时被驱逐复用），Broker 静默丢弃旧 ACK，绝不影响当前正在进行的租约。

### 6.3 慢算法丢帧背压与崩溃自愈

- **慢消费者隔离 (Drop Latest)**：Broker 跟踪各客户端在途处理帧数（`in_flight_count`，上限默认为 2）。当达到上限时，Broker 直接对该客户端丢弃最新帧，绝不阻塞主视频流或其他高速算法包；
- **子进程崩溃自愈**：当检测到客户端 Socket 断开或收到 `POLLHUP` 时，Broker 立即扫描并回收该客户端在所有槽位上的未归还租约，槽位引用清零后自动恢复为 `FREE`，彻底杜绝槽位死锁与泄漏。

### 6.4 IPC 协议自然对齐与 Socket 鲁棒性规约

- **严格 8 字节自然对齐**：IPC 二进制包中 64 位字段（`generation`、`stream_id`、`frame_id`、`pts`、`capture_time_ms`）必须排布在 8 字节自然对齐偏移上；`FrameDispatchMsg` 包含 `int64_t capture_time_ms`（帧物理采样绝对 UTC 毫秒时间戳），总尺寸 96 字节，编译期 `static_assert(offsetof(...) % 8 == 0)` 与总大小校验，杜绝嵌入式体系下非对齐访问与时钟时序漂移。
- **四态 Socket 状态机与半包熔断**：流读取使用四态机制（`SUCCESS`、`IDLE_TIMEOUT`、`CLOSED`、`DESYNC_ERROR`）。半包超时或错位直接置为 `DESYNC_ERROR` 并主动断开连接。
- **发送超时隔离与最大 Payload 门禁**：服务端套接字配置 50ms `SO_SNDTIMEO` 发送超时；IPC 消息强制执行 `MAX_IPC_PAYLOAD_SIZE = 64KB` 尺寸检查与线程级 `try ... catch` 异常防爆门。两侧常量与 Go 侧 `MaxIpcPayloadSize` 保持一致。
- **在途配额原子回滚**：槽位写入异常或 Socket 发送失败时，原子回退预分配的 `in_flight_count`、`delivered_frames`，并记入 `dropped_frames`。
### 6.5 DMA-BUF 硬件零拷贝与 SCM_RIGHTS 传输契约

- **跨进程句柄透传**：在支持 DMA-BUF 的边缘平台（如 RK3568），宿主 `FrameBroker` 在槽位分配时通过 `DmaBufAllocator` 预分配物理连续内存（CMA/System Heap）。通过 Unix Domain Socket 发送 `MSG_FRAME_DISPATCH` 时，利用 `SCM_RIGHTS` 辅助数据（`sendmsg`/`recvmsg`）将底层 DMA-BUF 引用文件描述符安全复制到 Runner 进程。
- **FD 接收与安全闭环**：
  - Runner 进程利用 `recvmsg` 提取 `SCM_RIGHTS` fd，并自动标记 `FD_CLOEXEC`（`recv_msg_with_fd`）；
  - 提取的 fd 赋值入 `zhulong_frame_view.dma_buf_fd` 暴露给插件；
  - ⚠️ **严禁句柄泄漏**：Runner 进程在完成单帧推理并发出 `MSG_FRAME_ACK` 后，**必须立刻 close 接收到的局部复制 fd**。FrameBroker 端负责物理 slot 的持有与释放，两侧生命周期解耦。
- **CPU Cache 同步守则**：DMA-BUF 在 CPU 软件访问前必须执行 `DMA_BUF_IOCTL_SYNC` 带 `DMA_BUF_SYNC_START`；硬件外设（RGA/NPU）读取前执行 `DMA_BUF_SYNC_END` 刷写脏缓存行，确保外设读取到最新像素。
- **透明优雅降级**：非 Linux 环境或无 `/dev/dma_heap` 支持时，`dma_buf_fd` 恒为 `-1`，系统透明降级至纯 POSIX 共享内存与 CPU 软处理，上层无感。

---

## 7. 元数据约束、底库同步与共享注册表

> 算法包物理目录结构、打包格式与完整 `manifest.json` 规则参见 [算法包开发与交付规范](../backend/algorithm-guidelines.md)。

### 7.1 IPC 握手与安装路径安全边界

1. **`id` / `version` 路径安全**：作为物理路径拼装参数，必须满足白名单正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`，严禁包含 `../` 逃逸路径。
2. **`name` 握手定长缓冲上限**：`name` 会作为 `--name` 传给 runner 并拷入 `HandshakeReq.client_name[32]`。必须满足非空且 **UTF-8 字节长度 ≤ 32**，超过时拒绝验包，防止 `strncpy` 静默截断造成多算法命名碰撞。

### 7.2 底库热更新载荷字段名映射

Go 侧与 Runner 进程之间 IPC 载荷统一使用 `features` 字段，表示可变 $D$ 维 `float32` 特征向量（人脸模型通常为 512 维）。人员领域模型与 IPC 载荷统一对齐为 `features`，消除了早期的 `feature_vector` 命名漂移。

### 7.3 底库热更新链路规范

| 维度 | 规范与实现 |
| :--- | :--- |
| **触发时机** | 人员/人脸 CRUD 成功后；冷启动时由 `algorithm.Supervisor` 异步触发全量同步 |
| **传输通道** | 复用 `FrameBroker` 点对点 Unix Domain Socket 连接，经 `Engine::broadcast_ai_message` 下行广播 |
| **消息类型** | `MSG_GALLERY_SYNC` (10)、`MSG_GALLERY_UPSERT` (11)、`MSG_GALLERY_DELETE` (12)、`MSG_GALLERY_ACK` (13) |
| **ACK 语义** | 全量 `MSG_GALLERY_SYNC` 组装完成后回复 `MSG_GALLERY_ACK`；增量同步无需 ACK |
| **单条上限与分片** | 单条上限 64KB。Go 侧按 10 项/片切分分片，Runner 维持 5 秒组装窗口，收齐后原子应用并清空旧底库；超时或乱序显式报错 |
| **身份字段** | 载荷 `person_id` 恒取 `persons.person_id`（对外业务键）**原值**，严禁混用内部行 ID |
| **本地索引** | Runner 进程内通过 `GalleryIndex` 维护 `face_id -> FaceTemplate` 映射，写操作受互斥锁保护，推理读取通过 `snapshot()` 获取不可变只读视图 |

### 7.4 进程内跨库底库共享注册表 (`GalleryRegistry`)

由公共算法运行库 `libzhulong_algo_runtime.so` 导出的全局单例 `zhulong::runtime::GalleryRegistry`，为同一进程空间内的 Runner 宿主与脱机插件提供无锁读写隔离的底库模板快照访问：

- **单例拓扑**：Runner 进程与插件动态库均动态链接 `libzhulong_algo_runtime.so`，共享唯一注册表实例；
- **读写隔离**：Runner 收到 `MSG_GALLERY_*` 后在写锁保护下更新底库并原子重构快照；
- **插件借用**：插件在 `detect_persons` 热路径中通过 `zhulong::GalleryRegistry::instance().snapshot()` 获取只读视图，零锁争用；
- **1:N 特征比对**：计算检出人脸 512 维特征与快照条目的余弦相似度（点积），Top-1 相似度 $\ge \text{threshold}$ 时判定命中并装填 `person_id`。

---

## 8. 异构硬件加速后端 (RKNN / RGA)

公共运行库 `libzhulong_algo_runtime.so` 针对异构硬件（如 Rockchip RK3568）提供了编译期可选的 RKNN 推理加速与 RGA 视觉前处理硬件加速。详细的编译配置、依赖隔离、真机实测踩坑规约（如 RKNN 2.3.2 下 `MEM_SIZE` 陷阱、权重共享与 RGA 防 DMA 越界挂死规则）详见独立规范：

👉 [原生硬件加速后端规范 (Hardware Backends: RKNN & RGA)](./hardware-backends.md)

---

## 9. 推理结果跨进程上报契约 (Inference Result Egress Contract)

打通算法推理结果由 **Runner 子进程 ➔ FrameBroker (UDS) ➔ ZhulongEngine (C ABI) ➔ Go 宿主 ➔ 业务消费** 的完整回路。

### 9.1 传输拓扑与通道复用

- **通道复用**：Runner 通过与 FrameBroker 已建立的 Unix Domain Socket 发送 `MSG_RESULT = 7` 消息，复用既有连接与握手流程。
- **全双工时序**：Broker 负责向 Runner 派发帧（`MSG_FRAME_DISPATCH`）；Runner 推理完成后先上报 `MSG_RESULT`，随后发送 `MSG_FRAME_ACK`。

### 9.2 载荷格式与坐标契约

- 载荷采用自描述 JSON 字符串编码，包含 `stream_id`、`pts`、`capture_time_ms`（帧抓拍绝对物理毫秒时间戳）、`cost_ms`、`task_type` 以及多态结果数组（`detections`、`classifications`、`persons`）。
- **人脸识别方案 B 扩展字段**：当检出人脸并命中底库时，根层或人脸子项如实携带 `matched_face_id`（Top-1 胜出人脸底照 ID）及 `candidates[].face_id`，为宿主落盘 `gallery_image_path` 与持久化提供唯一确凿的底照证据溯源。
- **坐标恒定归一化**：所有检测框与人体/人脸位置恒定采用 `[x1, y1, x2, y2] ∈ [0.0, 1.0]` 浮点数，与前端/Go 领域模型严格对齐。

### 9.3 状态机过滤规则（防洪峰策略）

Runner 维持两态机进行自适应节流，避免连续空画面产生无效 IPC 与 CGO 开销：

1. **连续空目标帧完全静默**：若当前帧与上一帧检出目标数均为 0，不产生任何 IPC 消息；
2. **目标消失退出信标（Exit Beacon）**：从「检出目标数 > 0」转为「检出目标数 == 0」的第一帧，发送一帧目标列表为空的信标结果，通知前端清空滞留画框（杜绝 250ms 幽灵框）；
3. **有目标帧实时上报**：检出目标数 > 0 时，每帧如实上报。

### 9.4 内存所有权与生命周期

- **Native 侧生命周期**：

  ```cpp
  rc = api.process(task, &view, &result);
  if (result) {
      if (should_send) {
          std::string payload = serialize_result(result);
          send_ipc_message(MSG_RESULT, payload);
      }
      api.result_release(result); // 严禁持有 result 指针跨越到异步队列，严防 use-after-free
  }
  ```

- **CGO 边界契约**：
  - C ABI 回调 `Zhulong_ai_result_callback` 传递只读借用指针：`const char *json_payload, size_t payload_len`；
  - Go 侧在回调同步执行期间完成深拷贝（`C.GoBytes`），回调返回后 Native 内存失效；
  - 回调令牌使用 `cgo.Handle` 传递安全整数，严禁向 Native 传递 Go 指针。

### 9.5 慢消费背压与丢弃策略

- **丢最旧（Drop Oldest）**：Go 侧 `AIResultSubscription` 采用固定容量环形队列（默认容量 32）。
- 当队列满且慢消费者未及时取走时：丢弃最旧一帧、原子递增 `DroppedResults` 计数、新帧推入队列，绝不阻塞 Native 引擎与 FrameBroker 回调线程。
