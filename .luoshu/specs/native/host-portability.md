# 宿主可移植性：Linux / macOS 双平台原生构建 (Host Portability)

> 原生层在 Linux（RK3568 交叉 + x86 宿主）之外，必须能在 macOS 宿主上构建并通过 `make native-test`。
> 本文记录「把 Linux 事实当成世界事实」导致的四类真实缺陷，以及避免复发的写法约定。

---

## 1. 为什么要有这份规范

macOS 宿主是日常开发机，不是二等公民。一个只在 Linux 成立的假设，在 macOS 上会以**看起来像环境噪声的红测试**出现 ——
容易被归类为「本机问题」而绕过，实际是生产缺陷。判据是：**失败路径是否对应真实功能？**
`Acquire(ConsumerAI)` 返回 `ERR_IO`、`extract` 永远 `UNSUPPORTED`、空 buffer 声称同步成功 —— 这三条在真机上一样坏。

⚠️ **不要把红测试按平台分档**。先判断缺陷是否与平台无关，再决定是否加平台条件。

---

## 2. 四类已发生的平台假设缺陷

### 2.1 Linux 专有挂载点不得进入无条件路径集合

| 事实 | 位置 |
| --- | --- |
| `/dev/shm` 是 Linux 的 `shm_open` 挂载点，macOS 不存在 | `native/src/algorithm/ipc/broker_resource_lock.cpp` |
| Go 侧清扫器在非 Linux 平台本就是 no-op（`internal/algorunner/orphan_other.go`） | 与上面必须对称 |

`FrameBroker::start` 曾固定锁 `{base_dir, "/dev/shm"}`；`stat("/dev/shm")` 失败 → `BrokerResourceLock::acquire`
返回 `nullptr` → 整个 broker 创建失败 → 上层翻译成 `Zhulong_ERR_IO`。

**约定**：目录集合的唯一来源是 `BrokerResourceLock::resource_dirs(base_dir)`，生产代码与 C++ 测试都调它，
不得各自硬编码字面量。平台差异用一个 `constexpr const char* kShmSweepDir`（非 Linux 为 `nullptr`）表达。

**推理链**：没有清扫方 → 没有需要互斥的对象 → 创建侧不该去锁一个不存在的目录。**不是「忽略错误」，是「不锁」。**

### 2.2 所有权/有效性检查必须在条件编译之外

```cpp
// ✗ 错：非 Linux 分支直接 return true，空对象声称同步成功
bool DmaBuffer::sync_start(uint64_t flags) {
#if defined(__linux__)
    if (fd_ < 0) return false;
    ...
#else
    return true;
#endif
}

// ✓ 对：守卫提到条件编译之外
bool DmaBuffer::sync_start(uint64_t flags) {
    if (fd_ < 0) return false;      // 不能对自己不拥有的 fd 报告成功 —— 与平台无关
#if defined(__linux__)
    ...
```

`sync_start` 与 `sync_end` 都犯过同一个错。**判据**：把 `#if` 两侧分支的差异写下来，问「哪些前提对两个平台都成立？」
成立的部分一律上移。

### 2.3 共享库后缀、可执行后缀从单一常量派生

| 平台 | 插件后缀 |
| --- | --- |
| Linux | `.so` |
| macOS | `.dylib` |

`FacePluginLoader` 的候选路径表曾全部写成 `.so`，macOS 上 `dlopen` 永远 miss → `extract` 一路 `UNSUPPORTED`，
把「插件不可用」伪装成正常业务返回。**约定**：候选名由 `kPluginExt`
（`native/src/abi/engine.cpp`，`__APPLE__` → `".dylib"`，否则 `".so"`）拼装；任何新增插件加载器同样处理。

### 2.4 内核 socket 缓冲区默认值不能假设

| 平台 | `AF_UNIX` 默认 `SO_SNDBUF` |
| --- | --- |
| Linux | ≈ 200KB |
| macOS | 8KB |

协议单包上限 `MAX_IPC_PAYLOAD_SIZE` 是 64KB。macOS 上默认缓冲装不下一整条报文，`sendmsg` 撞上 50ms
`SO_SNDTIMEO` 返回失败 —— **协议内合法的报文被误判成发送失败**。

**约定**：accept 后按 `MAX_IPC_PAYLOAD_SIZE * 2` 显式设置 `SO_SNDBUF`。⚠️ 只调上限，**不放宽超时** ——
慢消费者语义（50ms 拒绝）必须保持，否则背压会静默消失。

---

## 3. Go 侧的对称性要求

平台相关的 Go 实现通过**文件后缀**分档（`*_linux.go` / `*_other.go`），原生侧的对应逻辑必须与之语义对齐：

| C++ | Go |
| --- | --- |
| `kShmSweepDir` 在非 Linux 为 `nullptr` | `orphan_other.go` 的清扫为 no-op |
| 目录锁集合来自 `resource_dirs()` | `resource_lock_linux.go` 的 `fileID`（major/minor/inode） |

两侧锁必须是同一批对象、同一加锁顺序，否则 flock 与清扫器之间会出现竞态。改动任何一侧都要同步另一侧。

---

## 4. 验证方法

```bash
# 宿主原生回归：24 项 CTest，含 C ABI / broker / IPC / dmabuf / 插件加载
make native-test

# Go 桥接集成（native 侧的 ERR_IO / payload 行为会在此暴露）
make go-check
```

- Linux 主机全绿 **不** 等于 macOS 全绿，反之亦然；两个宿主各跑一次才算覆盖。
- 交叉编译只验证「能编译」，不验证平台假设（`CROSS_PROFILE` + `CROSS_OUTPUT`，禁止执行测试）。

---

## 5. 新增平台相关代码时的自检清单

- [ ] 是否引入了只有某个平台存在的**路径**（`/dev/shm`、`/proc`、`/sys`）？非本平台走什么分支？
- [ ] 平台差异是否收敛到一个具名常量 / 单一函数（`kPluginExt`、`resource_dirs`），而不是散落的 `#if`？
- [ ] 条件编译的**公共前提**（fd 有效性、空指针、参数校验）是否在 `#if` 之外？
- [ ] 是否依赖了某个内核/系统的**默认值**（缓冲区大小、超时、路径长度 `sun_path`）？显式设置了吗？
- [ ] Go 侧的平台分档实现是否需要同步修改？
- [ ] 两个宿主都跑过 `make native-test` 了吗？
