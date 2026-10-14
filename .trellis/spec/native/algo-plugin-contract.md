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

全系统（通用目标检测、人体/人脸结构化、IPC 消息、Go 宿主领域模型与前端渲染）统一采用 `[x1, y1, x2, y2] ∈ [0.0, 1.0]` 严格归一化两点坐标，全面废弃 `(x, y, w, h)` 语义。
结果内存由插件创建，并只能通过同一插件的 `result_release` 释放。添加分类与结构化能力保持 v1 函数表与 `zhulong_frame_view` 布局稳定（数据层多态）。
结构化结果采用**单块连续 Arena 内存打包（Contiguous Arena Packing）**，所有嵌套子指针指向 Arena 内部偏移，`result->internal_token` 指向首地址，`result_release` 通过单次 `std::free` 彻底释放整棵树，杜绝内存碎片与嵌套泄漏。
结构化坐标严格归一化到 `[0.0, 1.0]`，关键点（5 点）同样严格归一化。纯人脸检测场景下采用退化语义：每个检出人脸映射为一个 `zhulong_person_item`（`track_id=0`，`body_box=face_box`，`body_score=1.0`）。

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

class MyPersonStructurePlugin {
public:
    zhulong_status_t init(const std::string& config_json);
    zhulong::Session create_session(uint32_t worker_id);
    std::vector<zhulong::PersonItem> detect_persons(zhulong::Session&, const zhulong::Image&);
};
ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(MyPersonStructurePlugin, "MyPersonStructure", "1.0.0")
```

Detector 宏调用 `detect()` 并序列化 bbox；Classifier 宏调用 `classify()` 并序列化 classification item；PersonStructure 宏调用 `detect_persons()` 并将多级嵌套树打包为单块连续 Arena 内存。三者共享 opaque handle lifecycle 和 exception boundary，但使用各自的 result type 与 payload。

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

### 5.1 Runner 命令契约：`--broker` 参数语义

`zhulong_algo_runner --plugin <plugin.so> --broker <socket_path> [--broker <socket_path_2> ...] [--name <client_name>] [--max-frames <N>]`

- **`--broker` 的语义为字面 socket 路径**：客户端直接执行 `connect(path)`，不做目录派生。支持重复传入多个 `--broker` 参数以订阅多路相机流。
- **Native Engine 作为 Broker 宿主**：当流具有 `ConsumerAI` 消费者时，Native `Engine` 自动实例化并启动 `ipc::FrameBroker`，生成的 socket 路径为 `<base_dir>/zhulong_broker_<stream_id>.sock`。
- **`ZHULONG_FRAME_BROKER_SOCK` 环境变量**：仅在 Native 端作为 `base_dir` 读取（默认 `/tmp`），Go 侧不再读取该变量。
- **路径解析与传递**：Go 侧通过 C ABI `Zhulong_stream_get_broker_path`（Go 封装为 `Stream.BrokerPath(ctx)`）获取各相机流的实际 socket 路径，并经 `BrokerSubscriptionResolver` 组装为 `--broker <socket_path>` 列表传给 runner。该接口在解析路径的**同时**同步订阅关系（哪一路 AI 流因哪个包而保活），因此命名刻意不用 `Resolve*`。
- **⚠️ runner 从 socket 路径反解 stream_id**：runner 需要 `task_id` 来区分多路 task state，但 IPC 握手协议**没有**承载 stream_id。约定：runner 按 `<base_dir>/zhulong_broker_<stream_id>.sock` 从**字面 `--broker` 路径**反解 stream_id 并用作 `task_id`（`runner.cpp` 的 `parse_stream_id_from_socket`）。
  - 因此 **`--broker` 传入的路径必须遵循宿主的派生规则**，否则 runner 以 exit 5 显式报错（`stage:"args"`），不会静默退化。
  - 这是**唯一**允许 runner 依赖 socket 路径命名格式的地方；若将来需要更强的解耦，应改为在 `HandshakeReq` 中显式携带 stream_id（需 `ipc_protocol.hpp` 版本演进）。

### 5.2 Runner 退出码表

`runner.cpp` 的 `run_ipc_workers` 路径在各类失败时返回固定退出码，运维与 Go 看门狗依赖该表区分故障类型：

| 退出码 | 含义 | 出处 |
| --- | --- | --- |
| `0` | 正常退出 | `runner.cpp` |
| `1` | `dlopen` 插件动态库失败 / 客户端名称超长 | `runner.cpp` |
| `2` | `model_create` 失败 | `runner.cpp` |
| `3` | `worker_create` 失败 | `runner.cpp` |
| `4` | `task_open` 失败 | `runner.cpp` |
| `5` | **broker 连接失败 / 无法从 `--broker` 路径反解 stream_id / 未提供 `--broker`**（任一路失败即整体失败） | `runner.cpp` |

⚠️ 多路 `--broker` 下，任一路连接失败即整体失败，退出码严格保持为 `5`，且 stderr 输出伴随失败路索引与字面路径：`{"status":"failed","stage":"connect","index":N,"path":"...","error":"..."}`。

⚠️ **D6 的实际语义强于「整体失败」**：任一路失败会置位 `stop_all`，**已连上的路也会被一并终止**（不会继续跑成半瘫）。因此不存在「部分连接成功」的中间态，Go 侧只需按 `exitCode == 5` / `stage:"connect"` 单一通道识别。

⚠️ `1` 同时覆盖 `dlopen` 失败与 **client name 超长**（`HandshakeReq.client_name[32]`）：多路时命名会追加 `#<idx>` 后缀，加上后缀后超长即显式报错，绝不静默截断。

### 5.3 FrameBroker 宿主与帧发布通路已实现说明

FrameBroker 宿主与帧发布通路已完整闭环（任务 `10-13-algo-broker-host`）：
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

### 6.4 IPC 协议自然对齐与 Socket 鲁棒性规约 (IPC Alignment & Socket Hardening)
- **严格 8 字节自然对齐**：IPC 二进制包（如 `FrameDispatchMsg`、`FrameAckMsg`）中 64 位字段（`generation`、`stream_id`、`frame_id`、`pts`）必须排布在 8 字节自然对齐偏移上；结构体总尺寸必须为 8 的倍数并包含尾部对齐填充；必须有编译期 `static_assert(offsetof(...) % 8 == 0)` 与总大小校验，彻底杜绝 ARM64/嵌入式体系下非对齐访问与 `-Waddress-of-packed-member` 警告。
- **四态 Socket 状态机与半包熔断**：流读取使用四态机制（`SUCCESS`、`IDLE_TIMEOUT`、`CLOSED`、`DESYNC_ERROR`）。空闲轮询超时允许平滑重试，但若在已收到部分字节后发生超时或错误，必须标记为 `DESYNC_ERROR` 并立即主动断开连接，禁止在错位的字节流上继续读取。
- **发送超时隔离与最大 Payload 门禁**：服务端套接字必须配置 50ms `SO_SNDTIMEO` 发送超时，防止卡死/失联子进程反向阻塞主媒体捕获与解码流水线；IPC 消息必须强制执行 `MAX_IPC_PAYLOAD_SIZE = 64KB` 尺寸检查与线程级 `try ... catch` 异常防爆门，隔离异常子进程。**Go 侧同样受此门禁约束**：`internal/algorithm/process.go` 的 `EncodeIpcMessage`（发送侧）与 `DecodeIpcHeader`（接收侧）必须对 `PayloadSize > MaxIpcPayloadSize` 返回错误，两侧常量必须与 native `ipc_protocol.hpp:11` 的 `MAX_IPC_PAYLOAD_SIZE` 保持一致（由 `internal/algorithm/ipc_contract_test.go` 跨语言比对）。
- **在途配额原子回滚**：`publish_frame` 在共享内存槽位写入异常或客户端 Socket 发送失败时，必须原子回退预分配的 `in_flight_count`、`delivered_frames`，并记入 `dropped_frames`，防止在途配额死锁导致后续视频帧被永久丢弃。
- **多平面 (Multi-planar) 逐行安全复制**：解码器传递多平面帧（如 `YUV420P`、`NV12`）时必须携带完整的 `host_planes[4]` 与 `plane_strides[4]`；FrameBroker 写入共享内存时必须根据格式分平面逐行拷贝，严禁将 `data[0]` 假定为单一连续缓冲区进行越界 `memcpy`。



---

## 7. `manifest.json` 字段约束与安装路径安全

### 7.1 标识字段约束（仅 id/version 为路径承载字段）

`manifest.json` 的 **`id` / `version`** 会被 Go 侧用于拼装安装路径
`<installDir>/<id>/<version>`。若允许路径分隔符或 `..` 点段，`filepath.Join` 会被逃逸出 `installDir`（已实测确认：
`filepath.Join("/data/algorithms", "../../../tmp/pwned", "1.0.0")` → `/tmp/pwned/1.0.0`）。

因此 **`id` 与 `version`** 必须满足以下白名单正则（Go 侧 `internal/algorithm/verifier.go` 强制）：

```
^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$
```

约束含义：

- 首字符必须为字母或数字，杜绝以 `.` / `-` / `/` 开头；
- 允许字符集仅 `A-Za-z0-9._-`，禁止路径分隔符 `/`、`\`、空格、Unicode；
- 总长度 1–64 字符。

校验失败返回领域错误码 `INVALID_MANIFEST_TOKEN`。除字符集白名单外，Go 侧在拼装 `targetDir` 后还会执行一次
绝对路径前缀断言，确保结果始终被约束在 `installDir` 之内（纵深防御）。

#### `name` 字段：不做字符集限制，但有字节上限

⚠️ **`name` 不是路径承载字段**，**不受**上述白名单约束。它是人类可读的展示名（如 `安全帽佩戴检测算法`），
若施加该白名单会拒绝所有非 ASCII 名称。

但 `name` 会作为 `--name` 传给 runner，并被拷入定长缓冲 `HandshakeReq.client_name[32]`
（`ipc_protocol.hpp:37`，拷贝处 `runner.cpp:201`）。`strncpy` 会**静默截断**，导致两个算法包共享同一 client name。
因此 `name` 必须满足：

- **非空**；
- **UTF-8 字节长度 ≤ 32**（超过则返回 `INVALID_MANIFEST_TOKEN`，而非静默截断）。

Go 侧对应常量：`manifestNameMaxBytes = 32`。

### 7.2 底库热更新载荷字段名映射

Go 侧 `internal/algorithm/process.go` 的 `GallerySyncItem.Features`（JSON `features`）是 IPC 载荷的权威字段名，
语义为可变 $D$ 维 `float32` 特征向量（具体维度由当前算法包元数据 `manifest.json` 或模型输出形状定义，常见如 128、256、512、768、1024 维，当前默认人脸模型以 512 维为例）。
人员领域 DTO `internal/person/model.go` 的 `GallerySyncItem.Features`（JSON `features`）已与之一并对齐统一（消除早期 `feature_vector` 命名漂移）。
IPC 载荷序列化时统一使用 `features`。
（注：`Face` 数据库实体由于映射 SQLite 动态 BLOB 列，依然使用 `FeatureVector []byte`，天然支持任意维度存储）。

### 7.3 底库热更新链路澄清（未实现，登记供后续任务）

`MSG_GALLERY_SYNC` / `MSG_GALLERY_UPSERT` / `MSG_GALLERY_DELETE` 的 Go 侧构造函数与
`person.GetGallerySyncData` 均为**未被调用的死代码**，native 侧也**没有**对应消息类型。以下为澄清结论，
供实现该链路的任务使用（当前归属见 `10-12-algo-lifecycle-remediation` R6）：

| 维度 | 结论 |
| --- | --- |
| **触发时机** | 人员/人脸 CRUD 成功后（`person.Service` 的 Create/Update/Delete/AddFace/DeleteFace）；冷启动时在 runner 就绪握手后全量同步 |
| **传输通道** | **待定** —— 依赖 FrameBroker 宿主归属（见 §5.3）。若宿主在 native engine 内，gallery 通道宜与 broker 复用同一 socket（多路复用消息类型）；若宿主在 Go 内，Go 可直接作服务端。该决策随 `10-10-algorithm-plugin-architecture` 一并做出 |
| **消息序列** | 全量 `SYNC`（冷启动）→ 增量 `UPSERT`/`DELETE`（运行期）；`SYNC` 后须有 `ACK`，增量可异步 |
| **ACK 语义** | 复用既有 `MSG_FRAME_ACK` 的序号机制，或新增 `MSG_GALLERY_ACK`（待宿主定案） |
| **单条上限与分片** | 受 §6.4 的 64KB 门禁约束。全量同步在人员规模增长后必然超限，**分片是必须的**——但分片协议需 native 侧支持（分片序号、总片数），随该链路实现 |
| **消息类型对齐** | native `ipc_protocol.hpp:14-20` 目前止于 `MSG_DISCONNECT=6`，Go 侧预留的 10/11/12 **尚未定义**。实现前必须先在 native 头文件补齐 |
| **身份字段** | 载荷 `person_id` 恒取 `persons.person_id`（对外业务键）**原值**——该键必填、唯一、不可变，因此**不存在回退到内部主键 `persons.id` 的分支**。注意 `person_faces.person_id` 是内部行 ID 外键、与载荷 `person_id` 同名但语义不同，二者严禁混用，否则 native 侧会同时收到两套标识符空间 |

---

## 8. RKNN 推理后端合同（Rockchip，已实现）

任务 `10-13-algo-rknn-inference-backend` 交付。本节的每一条都来自 **RK3568 真机实测**（Debian 11 arm64 / 内核 5.10.226 / `librknnrt.so` **2.3.2** / RKNPU 驱动 **0.9.8**），不是文档推断。

### 8.1 编译期后端选择（两个正交开关）

后端在**编译期**由 CMake cache 变量选定，不引入运行期 `dlopen` 后端分流：

| 变量 | 取值 | 说明 |
| --- | --- | --- |
| `ZHULONG_INFERENCE_BACKEND` | `cpu`（默认）\| `rknn` | 推理后端 |
| `ZHULONG_VISION_BACKEND` | `cpu`（默认）\| `rga` | 视觉前处理后端（RGA 见 §9） |
| `ZHULONG_RKNN_ROOT` | 路径 | 仅 `rknn` 需要；须含 `include/rknn_api.h` 与 `lib/librknnrt.so` |
| `ZHULONG_RGA_ROOT` | 路径 | 仅 `rga` 需要；须含 `include/rga/im2d.h` 等与 `lib/librga.so` |

- `rknn` 缺 `ZHULONG_RKNN_ROOT`、或前缀缺任一文件 → **`FATAL_ERROR`**（fail closed）。
- `rga` 缺 `ZHULONG_RGA_ROOT`、或前缀缺任一文件 → **`FATAL_ERROR`**（fail closed）。
- 取值非法 → `FATAL_ERROR`。
- 两个开关**正交**：可「RKNN 推理 + CPU 前处理」，也可「CPU 推理 + RGA 前处理」，或「RKNN 推理 + RGA 前处理」（profile `native/profiles/rk3568-rknn-rga.json`）。

⚠️ **后端是编译期绑定，不是运行期可选**。`native/sdk/include/zhulong/algo.hpp` 通过 `#include <algorithm/runtime/runtime_factory.hpp>` 直接依赖 runtime 私有头，因此 `RknnInferenceBackend` **必须**编译进 `libzhulong_algo_runtime.so`（CMake target `ZhulongAlgoRuntime`），不能作为独立 `.so` 由插件单独加载。

⚠️ **`cpu` 与 `rknn` 必须用两个不同的交叉 profile**（`native/profiles/rk3568.json` / `rk3568-rknn.json`）。原因：板端示例插件走 `zhulong::Model::load("mock_detector.weights")`，在 rknn 后端下会 `rknn_init` 一个不存在的文件 → `model_create` 失败 → runner **exit 2**。故 **cpu 产物继续服务 mock/broker 板端回归，rknn 产物专供 RKNN 验证**。

### 8.2 目录与依赖隔离

`rknn_api.h` 只允许出现在 `native/src/backends/rknn/`（`directory-structure.md` §2 铁律 4）。当前 3 处引用：`rknn_types.hpp`、`rknn_error_map.hpp`、`rknn_inference_backend.cpp`。

CMake 中该目录的 `target_include_directories` 必须 `PRIVATE`。验收：

```bash
grep -rn '#include.*rknn_api.h' native/src/ native/sdk/ native/include/
# 只应命中 native/src/backends/rknn/
```

### 8.3 链接与产物依赖（AC2 / AC6）

| 产物 | 允许的 `NEEDED` |
| --- | --- |
| `libZhulongAlgoRuntime.so`（rknn 构建） | **必须**含 `librknnrt.so`；**绝不**含 `librknn_api.so` |
| 宿主 `Zhulong`（cpu **与** rknn 构建） | **绝不**含 `librknnrt.so`（宿主只静态链 `ZhulongEngine`，不碰 runtime `.so`） |
| 任意交叉产物 | **绝不**含开发机绝对路径（RUNPATH/RPATH） |

⚠️ **`librknn_api.so` 是陈旧残留，严禁链接**。板端 `/usr/lib` 同时存在 `librknn_api.so`（**1.5.2**，25 个 `rknn_` 符号）与 `librknnrt.so`（**2.3.2**，39 个）。误链接会在运行期出现难查的符号缺失。链接目标必须是 `librknnrt.so`（SONAME 就是 `librknnrt.so`，非 `.so.2`）。

⚠️ **`-Wl,-rpath-link,<rknn>/lib` 是必需的**（实测：去掉后链接报 `undefined reference to rknn_init` 等 8 个符号）。下游目标（`ZhulongRknnTests`、插件）链接 `libZhulongAlgoRuntime.so` 时，GNU ld 必须解析其传递依赖符号，而 `-L` 目录**不**参与 rpath-link 搜索。该选项只影响链接期解析，**不会**把 `librknnrt.so` 加进下游 `NEEDED`。

⚠️ **必须清空 `BUILD_RPATH`**：CMake 会为「构建树之外的链接库」自动加 BUILD_RPATH，把开发机绝对路径烧进要部署的 `.so`：

```cmake
set_target_properties(ZhulongAlgoRuntime PROPERTIES SKIP_BUILD_RPATH TRUE BUILD_RPATH "")
```

⚠️ BUILD_RPATH 与 `-rpath-link` **不冗余**（已用受控实验证明）：清空 BUILD_RPATH 后 ld 失去唯一的传递依赖解析途径，必须显式 `-rpath-link` 补上。

### 8.4 版本固定与 SHA-256 门禁

`native/scripts/build.py` 对 vendored 前缀（`native/vendor/rknpu2/`）做离线校验，不匹配即构建失败：

| 文件 | SHA-256 |
| --- | --- |
| `include/rknn_api.h` | `c48e11a6f41b451a5fd1e4ad774ea60252d3d94f78bee9b21ea3d21b21deba9a` |
| `lib/librknnrt.so` | `d31fc19c85b85f6091b2bd0f6af9d962d5264a4e410bfb536402ec92bac738e8` |

版本三元组（真机实测，必须一并记录）：

```txt
librknnrt.so  = 2.3.2 (429f97ae6b@2025-04-09T09:09:27)
RKNPU 驱动     = 0.9.8
内核          = 5.10.226 / Debian 11 arm64
```

⚠️ `native/vendor/rknpu2/lib/librknnrt.so` 被 `.gitignore` 的 `*.so` 规则忽略，需显式否定规则 `!native/vendor/rknpu2/lib/librknnrt.so` 才可提交。

### 8.5 ⚠️ `RKNN_QUERY_MEM_SIZE` 会破坏上下文（2.3.2 实测缺陷）

**这是本后端最重要的一条约束。**

**现象**：查过 `RKNN_QUERY_MEM_SIZE` 的上下文，其**首次** `rknn_dup_context` 必定 **SIGSEGV**（崩在 `librknnrt.so` 内部）。

最小 C 复现（绕过全部本项目代码，直接 `-lrknnrt`）：

| 上下文上执行过的查询 | `rknn_dup_context` |
| --- | --- |
| 无 | ✅ `ret=0` |
| `SDK_VERSION` | ✅ `ret=0` |
| `IN_OUT_NUM` / 输入输出 `ATTR` | ✅ `ret=0` |
| **`MEM_SIZE`** | ❌ **SIGSEGV** |
| `SDK_VERSION` + `MEM_SIZE` | ❌ **SIGSEGV** |

`MEM_SIZE` 之后 `rknn_inputs_set` / `rknn_run` / `rknn_outputs_get` **全部正常** —— 该污染**只影响 `rknn_dup_context`**。

**强制约束**：**主 root 上下文绝不调用 `RKNN_QUERY_MEM_SIZE`**。需要该数据时用**独立的一次性上下文**查询后立即销毁：

```cpp
rknn_context probe = 0;
if (rknn_init(&probe, path, 0, 0, nullptr) == RKNN_SUCC) {
    rknn_query(probe, RKNN_QUERY_MEM_SIZE, &asset->mem_size, sizeof(asset->mem_size));
    rknn_destroy(probe);
}
```

代价：`load_model` 期间峰值多一份权重（实测约 9MB，仅加载期）。这是为同时满足「记录 MEM_SIZE 证据」与「dup 共享」付出的必要代价。

⚠️ 本缺陷的**官方性质未定**（可能是 2.3.2 已知缺陷，也可能是未文档化的调用约束）。**不臆造官方结论**；升级 RKNN Runtime 后应重测该组合。

### 8.6 权重共享（`rknn_dup_context`）实测结论

**共享成立**（RK3568 真机，同一模型 4 个上下文）：

| 组 | 做法 | RSS 峰值 | 增量 |
| --- | --- | --- | --- |
| 基线 | 4 × 独立 `rknn_init` | 60024 KB | **+50.4 MB** |
| 候选 | 1 × `rknn_init` + 4 × `rknn_dup_context` | 28340 KB | **+14.3 MB** |

内存收益约 **3.5×**；两组输出逐元素一致。

⚠️ **`RKNN_QUERY_MEM_SIZE` 的逻辑值不能当物理占用**：两组的 `total_weight_size` 均为 `9348096`（逐 worker 相同）。真实物理差异只能由 RSS/PSS 体现。

⚠️ **`rknn_dup_context` 的派生必须串行化**（`dup_mutex`）：不假定该接口可并发调用。

### 8.7 资源模型与生命周期（R5）

```text
RknnModelAsset : ModelAsset          ← 每模型一个，进程内共享
  ├─ rknn_context root_ctx + bool root_valid
  ├─ std::mutex dup_mutex            ← 串行化 dup
  ├─ input_attrs / output_attrs      ← 从 Runtime 查询，不猜
  └─ mem_size / sdk_version          ← 证据记录

RknnSessionContext : SessionContext  ← 每 worker 一个，独占
  ├─ rknn_context ctx                ← rknn_dup_context 派生
  └─ std::shared_ptr<RknnModelAsset> model   ← 保证 session 早于 root 销毁
```

- 根上下文**保守保留**至所有派生实例回收完成。
- ⚠️ **失败路径不得 double-destroy**：`root_valid` 只在全部查询成功后置 `true`；所有提前返回点的 `rknn_destroy(root)` 都在此之前，故局部 asset 析构时 `root_valid == false`，不会二次销毁。
- 创建中途失败必须回滚已分配资源。

### 8.8 错误映射（14 个返回码全覆盖）

`RKNN_SUCC=0` → `OK`；`PARAM_INVALID`/`MODEL_INVALID`/`CTX_INVALID`/`INPUT_INVALID`/`OUTPUT_INVALID` → `INVALID_ARGUMENT`；`DEVICE_UNAVAILABLE`/`DEVICE_UNMATCH`/`TARGET_PLATFORM_UNMATCH`/`INCOMPATILE_*` → `NOT_SUPPORTED`（环境不匹配而非代码 bug）；`MALLOC_FAIL` → `OUT_OF_MEMORY`；`TIMEOUT` → `TIMEOUT`；`FAIL` 与未知值 → `INTERNAL`。

⚠️ 设备不可用映射为 `NOT_SUPPORTED` 而非 `INTERNAL`，便于上层区分「环境问题」与「代码 bug」。

### 8.9 Tensor 桥接（本任务接受拷贝）

⚠️ **不得声称本任务实现了零拷贝。**

- `rknn_input` **没有 stride 字段** → 传入 buffer 必须 packed（`Tensor` 的 `std::vector<uint8_t>` 天然 packed）。padded buffer 会**静默算错**（不是报错，是错结果）。
- 输出经 `want_float=1` 由 runtime 统一转 float32（避免手工反量化、不猜 `zp`/`scale`），再**拷贝**进 `Tensor`。
- 输出 shape 取 `output_attrs[i].dims[0..n_dims)` **原样**，不跳 batch 维。
- `rknn_outputs_release` 必须由 RAII guard 保证所有路径（含异常）恰好执行一次。
- 零拷贝的正确做法是 DMA-BUF（后续任务），不是让 `Tensor` 承担生命周期责任。

### 8.10 多 Worker 与设备并发

RK3568 是**单 NPU 核**。真机实测 1/2/4 worker 各 20 次推理：耗时 316.8ms / 374.1ms / 588.7ms —— **未随 worker 数线性提升**。

⚠️ **不得声称吞吐线性扩展**。收益方向是 CPU 前处理/后处理与 NPU 执行的**重叠**，不是 NPU 并行。**不启用** `RKNN_FLAG_ASYNC_MASK`。

### 8.11 测试与验证

```bash
# CPU profile 回归（不依赖板卡）
make native-test                                    # 期望 15/15

# 头文件隔离
grep -rn '#include.*rknn_api.h' native/src/ native/sdk/ native/include/

# 产物依赖矩阵（AC2/AC6）
readelf -d build/native/cross-rk3568-rknn/libZhulongAlgoRuntime.so | grep -E 'NEEDED|RUNPATH'
readelf -d build/targets/rk3568-rknn/Zhulong | grep NEEDED      # 不得含 rknn

# rknn 交叉构建
python3 native/scripts/build.py --profile native/profiles/rk3568-rknn.json build

# 真机（交叉只编译不执行；板端手动跑）
scp build/native/cross-rk3568-rknn/{ZhulongRknnTests,libZhulongAlgoRuntime.so} rk3568:/userdata/zhulong-test/
ssh rk3568 'cd /userdata/zhulong-test && LD_LIBRARY_PATH=$PWD ./ZhulongRknnTests <model.rknn>'
# 期望：=== failures=0 ===
```

⚠️ `ZhulongRknnTests` 仅在 `ZHULONG_INFERENCE_BACKEND=rknn` 时构建；交叉时**只编译、不注册 CTest**（板端手动执行）。
⚠️ `ZhulongRknnTests` 的 build-dir RUNPATH **刻意保留**（板端配合 `LD_LIBRARY_PATH` 使用）；产物卫生只要求清 `libZhulongAlgoRuntime.so` 上指向 `native/vendor/` 的那个。

---

## 9. Rockchip RGA 视觉前处理后端合同（已实现）

任务 `10-13-algo-rga-vision-backend` 交付。本节每一条均来自 **RK3568 真机实测**（Debian 11 bullseye arm64 / 内核 5.10.226 / RGA 驱动 v1.3.4 / `librga.so.2.1.0` API `v1.10.1_[10]`），不是文档推断。

### 9.1 编译期选择与依赖隔离

通过 CMake cache 变量 `ZHULONG_VISION_BACKEND=rga` 选定。
- 需要 `-DZHULONG_RGA_ROOT=<path>`，指向包含 `include/rga/im2d.h` 与 `lib/librga.so` 的前缀；缺失则 `FATAL_ERROR`。
- **头文件绝对隔离**：`rga/im2d.h` 等厂商头文件**仅允许**在 `native/src/backends/rga/` 引用，严禁泄露至公开 SDK、引擎核心或调度层。
- **链接隔离**：`libZhulongAlgoRuntime.so` 动态链接 `librga.so.2`；宿主 `Zhulong` 二进制**绝不**链接 `librga.so`（`readelf -d` 验证通过）。

### 9.2 运行期硬件探测与对齐约束

RGA 后端在启动时自动调用 `probe()` 执行运行期能力发现：
- **行跨度对齐（`byte_stride_align`）**：从 `/sys/kernel/debug/rkrga/hardware` 读取（RK3568 实测为 **4** 字节）。**严禁硬编码**；读取不到或未取到有效值时透明回退 CPU，不向硬件传递非法对齐参数。
- **ROI 偏移对齐（`offset_align`）**：通过在测试缓冲上调用 `imcheck_t` 动态探测（RK3568 实测为 **2** 像素）。
- **设备权限与部署约束**：`/dev/rga` 权限为 `root:video`（`crw-rw----`）。宿主或运行 Runner 的用户**必须加入 `video` 用户组**，否则驱动拒绝打开并自动回退 CPU。

### 9.3 格式能力矩阵与颜色转换语义

| 输入格式 | RGA 硬件支持 | CPU 回退行为 | 约束与内存布局 |
| --- | --- | --- | --- |
| `RGB24` / `BGR24` | ✅ 硬件支持 | ✅ 支持 | 紧凑或满足 4 字节行对齐；目标恒定 HWC 3 通道 UINT8 |
| `RGBA32` | ✅ 硬件支持 | ❌ NOT_SUPPORTED | 单平面 4 字节/像素，硬件完成 CSC 提取与缩放 |
| `NV12` | ✅ 硬件支持 | ❌ NOT_SUPPORTED | 仅支持**连续内存**（plane1 == plane0 + y_stride * height）；非连续内存安全回退 CPU 返回 `NOT_SUPPORTED` |
| `YUV420P` | ✅ 硬件支持 | ❌ NOT_SUPPORTED | 仅支持**连续内存**（plane1/plane2 紧密相连）；非连续内存安全回退 CPU 返回 `NOT_SUPPORTED` |
| `UNKNOWN` | ❌ 显式拒绝 | ❌ NOT_SUPPORTED | 返回 `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED` |

- **YUV 色彩空间转换**：硬件 CSC 采用 **Limited-range BT.601** 标准公式（YUV 16-235 映射至 RGB 0-255），实测与主流编解码软解管线一致。

### 9.4 防挂机契约与两阶段裁剪

- ⚠️ **严禁对目标缓冲区做指针偏移（OOB 越界防范）**：
  Letterbox 绘制时，如果将目标 ROI 指针（`dst_buf + roi_offset`）传递给 `wrapbuffer_virtualaddr`，RGA 驱动会基于该偏移指针按完整 `wstride × hstride` 校验与计算 DMA 访问范围，导致驱动访问超出 Tensor 内存边界（~242KB 越界），引发 RGA 硬件/驱动陷入 D 状态死锁并导致板端看门狗复位。
  **标准做法**：目标缓冲区恒定以全尺寸（`target_w × target_h`）包装为 `dst_buffer`，内容区偏移由 `improcess` 的 `drect`（`pad_left, pad_top, unpad_w, unpad_h`）表达，指针本身不偏移。
- ⚠️ **强依赖 `imcheck_t` 预校验**：每次调用 `improcess` 前必须使用 `imcheck_t` 校验 `srect` 与 `drect` 合法性，失败直接返回并在上层回退，杜绝向驱动提交非法任务。
- **两阶段裁剪（`crop_scale`）**：
  针对人脸识别/车牌两阶段算法，使用 `srect` 表达裁剪框。若 ROI 坐标不满足 2 像素对齐，采用**形式 1（改参数向外扩取整）**，避免任何 O(N) 内存搬移；适配生效时原子递增 `rect_adapted_count`。
- **Stride 适配**：行跨度大于紧凑宽度的图像，只要满足硬件 4 字节对齐且整除像素通道宽，直接设置 `wstride` 交由 RGA 处理，原子递增 `stride_adapted_count`。

### 9.5 滤波差异与容差（AC3）

RGA 硬件双线性插值单元与 CPU 软件基于中心对齐的浮点双线性滤波属于不同的核函数实现。下采样（1920x1080 → 640x640）时单像素极值绝对差最大可达 119。
- 测试验收标准采用 `worst <= 128` 断言结构一致性；Letterbox 补边区域恒定断言填充值 114。

### 9.6 性能与内存模型

- ⚠️ **不得声称零拷贝**：当前 RGA 后端走 `wrapbuffer_virtualaddr` 虚拟地址映射，存在用户态到 DMA 映射开销，未引入 DMA-BUF / dma_fence。
- 20 次连续调用真机实测 VmRSS 净增长为 0 KB（`delta=0 kb`），无句柄与内存泄漏。

---

## 10. 推理结果跨进程上报契约 (Inference Result Egress Contract)

打通算法推理结果由 **Runner 子进程 ➔ FrameBroker (UDS) ➔ ZhulongEngine (C ABI) ➔ Go 宿主 ➔ 业务消费** 的完整上行回路。

### 10.1 传输拓扑与通道复用
- **通道复用**：Runner 通过与 FrameBroker 已建立的 Unix Domain Socket 发送 `MSG_RESULT = 7` 消息，复用既有连接与握手流程。
- **全双工时序**：Broker 负责向 Runner 派发帧（`MSG_FRAME_DISPATCH`）；Runner 推理完成后先上报 `MSG_RESULT`，随后发送 `MSG_FRAME_ACK`。

### 10.2 载荷格式与坐标契约
- 载荷采用自描述 JSON 字符串编码，包含 `stream_id`、`pts`、`cost_ms`、`task_type` 以及多态结果数组（`detections`、`classifications`、`persons`）。
- **坐标恒定归一化**：所有检测框与人体/人脸位置恒定采用 `[x1, y1, x2, y2] ∈ [0.0, 1.0]` 浮点数，与前端/Go 领域模型严格对齐。

### 10.3 状态机过滤规则（防洪峰策略）
Runner 维持两态机进行自适应节流，避免连续空画面产生无效 IPC 与 CGO 开销：
1. **连续空目标帧完全静默**：若当前帧与上一帧检出目标数均为 0，不产生任何 IPC 消息；
2. **目标消失退出信标（Exit Beacon）**：从「检出目标数 > 0」转为「检出目标数 == 0」的第一帧，发送一帧目标列表为空的信标结果，通知前端清空滞留画框（杜绝 250ms 幽灵框）；
3. **有目标帧实时上报**：检出目标数 > 0 时，每帧如实上报。

### 10.4 内存所有权与生命周期
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

### 10.5 慢消费背压与丢弃策略
- **丢最旧（Drop Oldest）**：Go 侧 `AIResultSubscription` 采用固定容量环形队列（默认容量 32）。
- 当队列满且慢消费者未及时取走时：丢弃最旧一帧、原子递增 `DroppedResults` 计数、新帧推入队列，绝不阻塞 Native 引擎与 FrameBroker 回调线程。


