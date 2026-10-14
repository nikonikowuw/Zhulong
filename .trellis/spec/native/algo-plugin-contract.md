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
