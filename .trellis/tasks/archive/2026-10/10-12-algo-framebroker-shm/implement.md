# 实施计划 (Execution Plan)

## 1. 阶段顺序与检查点

### 阶段一：跨进程数据契约与 IPC 协议定义
- [x] 编写 `native/src/algorithm/ipc/ipc_protocol.hpp`：定义定长对齐的 `IpcHeader`, `HandshakeMsg`, `FrameDispatchMsg`, `FrameAckMsg` 等控制面数据结构；
- [x] 编写 `native/src/algorithm/ipc/shm_layout.hpp`：定义共享内存头部、槽位元数据与对齐常量；
- [x] 编写协议单元测试，验证跨平台 ABI 结构大小与对齐一致性。

### 阶段二：POSIX 共享内存环形槽位池封装
- [x] 编写 `native/src/algorithm/ipc/shm_pool.hpp` 与 `shm_pool.cpp`；
  - 主进程：创建、扩展 `ftruncate`、映射 `PROT_READ | PROT_WRITE`、清理 `shm_unlink`；
  - 子进程：打开、只读映射 `PROT_READ`、按槽位索引获取只读指针；
  - 析构与异常保护：RAII 自动 `munmap` 与 `close`。
- [x] 编写 SHM 单元测试，验证多进程/多线程写入与只读映射的一致性。

### 阶段三：FrameBroker 核心调度与租约代次状态机
- [x] 编写 `native/src/algorithm/ipc/frame_broker.hpp` 与 `frame_broker.cpp`；
  - 槽位状态机与代次管理（`SlotTracker`, `generation`, `active_leases`）；
  - `publish_frame(const HardwareFrame& frame)`：拷贝帧数据至空闲槽位，多客户端广播；
  - `handle_ack(client_id, slot_idx, generation)`：引用递减与 ABA 过期检查；
  - 客户端生命周期管理：注册、心跳、断连自愈与租约强退机制；
  - 慢客户端丢帧（Drop Latest）与槽位超时驱逐保底机制。
- [x] 编写 Broker 单元测试，验证租约代次保护与槽位流转。

### 阶段四：Unix Domain Socket 通信层与 Runner IPC 客户端
- [x] 编写 `native/src/algorithm/ipc/unix_socket.hpp` 与 `unix_socket.cpp`：提供非阻塞/事件驱动的 UDS 服务端与客户端通信抽象；
- [x] 在 `native/src/algorithm/runner/` 集成 IPC Client：
  - Runner 启动时连接 Broker，完成握手；
  - 监听 `MSG_DISPATCH`，从只读 SHM 槽位构造 `zhulong_frame_view`；
  - 调用插件标准 `process` 接口，执行完成后向 Broker 回传 `MSG_ACK`。

### 阶段五：工程集成、自动化测试与 Sanitizer 门禁
- [x] 将所有新增 target 与测试接入 `native/CMakeLists.txt`；
- [x] 编写端到端多进程集成测试 `native/tests/frame_broker_test.cpp`：
  - **测试场景 1**：多客户端并发租赁与多帧连续接收，验证数据完整性与 PTS 保真；
  - **测试场景 2**：慢客户端背压隔离，高速客户端 100% 满帧率，慢客户端稳定平滑丢帧；
  - **测试场景 3**：模拟客户端异常崩溃（`SIGKILL`），验证 Broker 在 100ms 内瞬间回收租约，零死锁；
- [x] 运行全量测试并执行 ASan / UBSan 与 TSan 门禁。

## 2. 验证命令
```bash
# 原生单元与集成测试
make native-test

# AddressSanitizer 与 UndefinedBehaviorSanitizer 门禁
python3 native/scripts/build.py --sanitizer address test

# ThreadSanitizer 门禁
python3 native/scripts/build.py --sanitizer thread test

# Go 全链路与原生回归
make go-check
```
