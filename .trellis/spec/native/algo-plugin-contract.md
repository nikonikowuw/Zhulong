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

所有插件通过导出的 `zhulong_plugin_get_api` 填充 `zhulong_plugin_api_v1`。v1 API 布局包含 `struct_size`、`api_version`、插件元信息和模型/Worker/Task/process/result-release 回调。检测、分类结果分别使用 `ZHULONG_RESULT_TYPE_DETECTION` + `zhulong_bbox[]`、`ZHULONG_RESULT_TYPE_CLASSIFICATION` + `zhulong_classification_item[]`；结果内存由插件创建，并只能通过同一插件的 `result_release` 释放。添加分类能力不得改变 v1 函数表或 `zhulong_frame_view` 布局。

插件入口声明使用 `ZHULONG_PLUGIN_EXPORT`。启用 hidden default visibility 时，只显式导出 `zhulong_plugin_get_api`；Linux 示例插件还使用 GNU ld version script 将其他定义符号设为 local（仅靠编译器 visibility 可能在 sanitizer 构建暴露弱 RTTI 符号）。CMake 示例插件目标定义 `ZHULONG_PLUGIN_BUILD` 并设置 hidden C++ / inline visibility。

### 异常防爆门守则
导出入口和 API callbacks 必须以 `try { ... } catch (...)` 阻止 C++ 异常穿越动态库边界；失败返回 `ZHULONG_PLUGIN_ERR_INTERNAL`，并在失败时将输出句柄/result 初始化为空。

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
```

Detector 宏调用 `detect()` 并序列化 bbox；Classifier 宏调用 `classify()` 并序列化 classification item。两者共享 opaque handle lifecycle 和 exception boundary，但使用各自的 result type 与 payload。

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

### Validation & Error Matrix

| 条件 | 结果 |
| --- | --- |
| 输入和目标格式均为 RGB24/BGR24，stride 为 0 或至少 `width * 3` | 成功 |
| 输入/目标格式包含尚未实现的格式 | `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED` |
| 空像素指针、非正图像尺寸、负 stride、stride 小于行字节数 | `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT` |
| 非空 Tensor shape 含零/负维度 | 抛出 `std::invalid_argument` |
| Tensor 元素数/字节数无法由 `size_t` 表示 | 抛出 `std::length_error` |

### Good / Base / Bad Cases

- **Good**：BGR24 解码帧以带 padding 的正 stride 输入，target_format=RGB24；逐行读取正确，输出通道为 RGB。
- **Base**：旧调用未填写 stride（零值）且缓冲为 packed RGB24/BGR24；按 `width * 3` 兼容处理。
- **Bad**：把 NV12 plane0 当三通道 RGB 读取，或 stride 小于 `width * 3`；必须在读取图像数据前拒绝。

### Tests Required

- `ZhulongAlgoRuntimeTests`：断言 padded stride 像素值、RGB/BGR 转换、零 stride fallback、多格式拒绝、双线性中心值、Letterbox padding、非法 stride 与 Tensor 异常。
- `ZhulongAlgoRunnerTests`：断言 detector/classifier `result_type` 和对应 typed payload。
- `ZhulongAlgoRunnerJson` / `ZhulongPluginSymbolVisibility`：解析含控制字符的 Runner JSON，并确认示例 `.so` 只导出 ABI 入口。

### Wrong vs Correct

**Wrong**：把 `src + (y * width + x) * 3` 用于任意解码帧，或对 classifier 返回 `DETECTION`/`zhulong_bbox[]`。

**Correct**：每行以 `src + y * src_stride` 定位，按声明格式采样/转换；classifier 注册使用 `ZHULONG_REGISTER_CLASSIFIER_PLUGIN` 并返回 classification payload。

## 5. 独立黑盒验包 CLI 工具 (`zhulong_algo_runner`)

验包命令：
```bash
zhulong_algo_runner --verify-plugin <path_to_plugin.so> --sample <test_image_or_mock>
```
输出严格 JSON 行。所有路径、错误信息、插件名称/版本和样本名称字符串字段都必须执行 JSON string escaping（引号、反斜杠、短控制字符及 U+0020 以下控制字节）；数值字段输出为 JSON number。JSON quoting helper 的序列化结果须由标准 JSON parser 回归验证。

```json
{"step":"start","plugin":"..."}
{"step":"abi_verified","name":"...","version":"..."}
{"status":"passed","sample":"...","result_type":1,"count":2}
```
退出码：`0` 表示 ABI、模型加载、Worker 创建、Task 打开、单次推理与内存释放全部符合规范。

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

### 6.4 IPC 协议自然对齐与 Socket 鲁棒性规约 (IPC Alignment & Socket Hardening)
- **严格 8 字节自然对齐**：IPC 二进制包（如 `FrameDispatchMsg`、`FrameAckMsg`）中 64 位字段（`generation`、`stream_id`、`frame_id`、`pts`）必须排布在 8 字节自然对齐偏移上；结构体总尺寸必须为 8 的倍数并包含尾部对齐填充；必须有编译期 `static_assert(offsetof(...) % 8 == 0)` 与总大小校验，彻底杜绝 ARM64/嵌入式体系下非对齐访问与 `-Waddress-of-packed-member` 警告。
- **四态 Socket 状态机与半包熔断**：流读取使用四态机制（`SUCCESS`、`IDLE_TIMEOUT`、`CLOSED`、`DESYNC_ERROR`）。空闲轮询超时允许平滑重试，但若在已收到部分字节后发生超时或错误，必须标记为 `DESYNC_ERROR` 并立即主动断开连接，禁止在错位的字节流上继续读取。
- **发送超时隔离与最大 Payload 门禁**：服务端套接字必须配置 50ms `SO_SNDTIMEO` 发送超时，防止卡死/失联子进程反向阻塞主媒体捕获与解码流水线；IPC 消息必须强制执行 `MAX_IPC_PAYLOAD_SIZE = 64KB` 尺寸检查与线程级 `try ... catch` 异常防爆门，隔离异常子进程。
- **在途配额原子回滚**：`publish_frame` 在共享内存槽位写入异常或客户端 Socket 发送失败时，必须原子回退预分配的 `in_flight_count`、`delivered_frames`，并记入 `dropped_frames`，防止在途配额死锁导致后续视频帧被永久丢弃。
- **多平面 (Multi-planar) 逐行安全复制**：解码器传递多平面帧（如 `YUV420P`、`NV12`）时必须携带完整的 `host_planes[4]` 与 `plane_strides[4]`；FrameBroker 写入共享内存时必须根据格式分平面逐行拷贝，严禁将 `data[0]` 假定为单一连续缓冲区进行越界 `memcpy`。


