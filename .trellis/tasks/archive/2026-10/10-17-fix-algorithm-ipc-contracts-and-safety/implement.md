# 实施计划：算法 IPC 帧分发与 Runner 契约修复

## 前置

- 任务已 `task.py create`，状态 `planning`。
- 设计文档：同目录 `design.md`（章节号在本文件中以 `§N` 引用）。
- 规范：`.trellis/spec/native/algo-plugin-contract.md`（§6 FrameBroker 契约）、
  `.trellis/spec/native/pipeline-guidelines.md`（§5 FramePublisher）、
  `.trellis/spec/native/index.md`（Pre-Development Checklist）。

### 硬性约束（每个阶段都要遵守）

- 绝不修改任何 `static_assert` 或结构体布局。
- 绝不修改 `internal/` 下任何 Go 文件。
- 改完 C++ 必须 `make native-build`（Air 只监听 Go/TOML）。
- 收工前过 `make check`。
- 每处改动可追溯到 PRD 的某条 REQ。

---

## 阶段 1：`write_all` 有界化 + 发送侧门禁（REQ-2、REQ-7）

> 先做这一阶段：它是「挂死级」缺陷，且改动局部、不依赖其他阶段。

- [ ] **1.1** `native/src/algorithm/ipc/unix_socket.cpp`：按 §3.2 重写 `write_all`，
      新增 `timeout_ms = 50` 默认参数，`EAGAIN` 走 `poll(POLLOUT, timeout_ms)`。
- [ ] **1.2** 同文件 `UnixSocketServer::send_msg_with_fd`：在 `IpcHeader header{}` 之前
      插入 §8.1 的双重门禁（`payload_size > MAX_IPC_PAYLOAD_SIZE` → `false`；
      `payload_size > 0 && payload == nullptr` → `false`）。
- [ ] **1.3** 同文件 `UnixSocketClient::send_msg_with_fd`：同样插入 §8.1 门禁。
      ⚠️ 位置必须在 `send_mutex_` 加锁**之前或之后皆可**，但要在 `write_msg_with_fd` 之前，
      且不得改变既有的「二次检查 fd」逻辑。
- [ ] **1.4** 更新 `write_all` 的 Doxygen 注释，写明 EAGAIN 语义与调用链影响面（§3.2 注释块）。

**验证项**

```bash
python3 native/scripts/build.py compile ZhulongAlgoIpc
```

- 编译零告警（该 target 带 `-Wall -Wextra -Wpedantic`）。
- `grep -n "EAGAIN" native/src/algorithm/ipc/unix_socket.cpp`：确认无任何
  `EAGAIN` 与 `continue` 直接配对（三处都必须是 poll 或 `return false`）。

---

## 阶段 2：`reserved_fd` 语义纠正（REQ-3）

- [ ] **2.1** `native/src/algorithm/ipc/ipc_protocol.hpp:107`：按 §4.2 更正 `reserved_fd`
      的 Doxygen 注释（**只改注释，不改类型/默认值/位置**）。
- [ ] **2.2** `native/src/algorithm/ipc/frame_broker.cpp:229`：`msg.reserved_fd = -1;`
      并加 §4.1 的说明注释。保留 `dma_fd_to_send` 作为 `send_msg_with_fd` 的实参。
- [ ] **2.3** `native/src/algorithm/runner/runner.cpp:574`：删除 `dispatch.reserved_fd` 回退，
      `active_dma_fd` 改为 `const int` 且只取 `received_fd`。

**验证项**

```bash
python3 native/scripts/build.py compile ZhulongAlgoIpc
python3 native/scripts/build.py compile zhulong_algo_runner
grep -rn "reserved_fd" native/src/
```

- 预期 `grep` 只剩：`ipc_protocol.hpp` 的定义+注释、`frame_broker.cpp` 的 `= -1` 赋值。
  **不得**再出现在 `runner.cpp`。

---

## 阶段 3：FrameBroker 容量门禁 + 统一回滚（REQ-1）

- [ ] **3.1** `native/src/algorithm/ipc/frame_broker.hpp`：在 `private:` 区、
      `find_or_evict_free_slot` 声明旁新增：

      ```cpp
      /**
       * @brief 发布失败的统一回滚：归还在途配额、释放槽位、记账，返回 false。
       * @param slot_idx 已分配的槽位索引（< 0 表示未分配，跳过槽位处理）
       * @param target_clients 已预分配配额的客户端列表
       */
      bool fail_publish(int slot_idx, const std::vector<uint32_t>& target_clients);
      ```

- [ ] **3.2** `frame_broker.cpp`：新增匿名 namespace 中的 `frame_payload_bytes(frame)`（§2.2）。
      ⚠️ NV12 用 `frame.y_plane_size()`，YUV420P 用 `stride * height`，**不可统一**。
- [ ] **3.3** 实现 `FrameBroker::fail_publish`（§2.4），内部自行加 `mutex_`。
- [ ] **3.4** 把既有「`slot_data` 获取失败」回滚块（约 :191-207）替换为
      `return fail_publish(slot_idx, target_clients);`。
- [ ] **3.5** 在 `slot_data`/`dma_slot_data` 获取成功之后、**任何 memcpy 之前**插入
      §2.3 的容量前置门禁（仅 `!is_zero_copy_frame` 时生效）。
- [ ] **3.6** 删除三处内层 `if (total_bytes <= options_.slot_size)` 条件（NV12 :239、
      YUV420P :281、BGR24 :326），直接执行 memcpy；把内层 `total_bytes` 局部变量删除
      （前置门禁已保证）。
- [ ] **3.7** `frame_broker.cpp:346` 的 `if (total_payload_bytes <= options_.slot_size)`
      改为无条件 `std::memcpy(dma_slot_data, slot_data, frame_payload_bytes(frame));`，
      并加 §2.5 的「DMA 槽位与 SHM 槽位同尺寸」耦合注释。

**验证项**

```bash
python3 native/scripts/build.py compile ZhulongAlgoIpc
grep -n "<= options_.slot_size" native/src/algorithm/ipc/frame_broker.cpp
```

- 预期 `grep` **零匹配**（所有容量判断都已收敛到前置门禁一处）。

---

## 阶段 4：`handle_ack` 校验前置（REQ-6）

- [ ] **4.1** `frame_broker.cpp:421`：按 §7.1 把 `ack.slot_index >= slots_.size()` 校验
      移到 `in_flight_count--` 之前。

**验证项**

```bash
python3 native/scripts/build.py compile ZhulongAlgoIpc
```

- 人工确认：函数体内第一条状态修改语句是校验之后的 `clients_.find`。

---

## 阶段 5：Runner `rc` 局部化 + 分片幂等（REQ-4、REQ-5）

- [ ] **5.1** `runner.cpp:434`：`zhulong_status_t rc` → `model_rc`，同步改 :435 的判断。
- [ ] **5.2** `runner.cpp:442` 的 lambda 体内（开头）新增局部 `zhulong_status_t rc = ZHULONG_PLUGIN_OK;`，
      并加 §5.1 的数据竞争说明注释。
- [ ] **5.3** `runner.cpp:674-690`：按 §6.1 重构为「范围校验 → 幂等 insert → 条件累加 items」。
      注意保持 `is_complete()` 分支内既有逻辑（`gallery_index.reset` + `MSG_GALLERY_ACK`）**逐字不变**。
- [ ] **5.4** `grep -n "rc" native/src/algorithm/runner/runner.cpp` 复查：
      `run_verify_plugin` 内的 `rc`（单线程）保持原样，**不动**。

**验证项**

```bash
python3 native/scripts/build.py compile zhulong_algo_runner
grep -n "auto run_single_worker\|zhulong_status_t rc\|zhulong_status_t model_rc" native/src/algorithm/runner/runner.cpp
```

---

## 阶段 6：测试补齐（REQ-8）

- [ ] **6.1** `native/tests/frame_broker_test.cpp` 新增 `test_frame_broker_slot_capacity_reject`：
  - `slot_count = 2`、`slot_size = 256 * 1024`（256KB）；
  - 构造 `stride = 640 * 3`、`height = 480`（payload = 900KB > 256KB）的 BGR24 帧；
  - 客户端连接并握手，记录 `total_dropped()` 初值；
  - 断言 `publish_frame(frame) == false`；
  - 断言客户端 `recv_msg(..., 200)` 超时（**未收到任何** `MSG_FRAME_DISPATCH`）；
  - 断言 `total_dropped()` 递增 1；
  - 断言该客户端 `in_flight_count == 0` 且 `delivered_frames == 0`、`dropped_frames == 1`；
  - 追加：换一个 128KB 的合法帧，断言 `publish_frame == true` 且客户端**能**收到
    `MSG_FRAME_DISPATCH`（证明门禁不误伤正常帧、且槽位已正确回到 `FREE`）。
- [ ] **6.2** 同文件新增 `test_frame_broker_malformed_ack_keeps_quota`：
  - 正常推一帧并收到 dispatch（此时 `in_flight_count == 1`）；
  - 发送 `slot_index = 0xFFFFFFFF`、`generation` 任意的畸形 ACK；
  - 断言 `in_flight_count` **仍为 1**（未被蚕食）；
  - 再发正确 ACK，断言回到 0（证明槽位仍可正常归还）。
- [ ] **6.3** `native/tests/ipc_socket_fd_test.cpp` 新增断言：
  - 在既有的「不带 fd 的常规消息」段落，用 `MSG_FRAME_DISPATCH` 载一个
    `FrameDispatchMsg`，断言 `received_fd == -1` **且** `dispatch.reserved_fd == -1`。
- [ ] **6.4** `native/tests/algo_runner_gallery_test.cpp` 新增：
  - `test_gallery_sync_shard_idempotent`：构造 `shard_total = 2` 的同一 `sync_id`，
    把 `shard_index = 0` 发送**两次**，再发 `shard_index = 1`；
    断言最终 `GalleryRegistry::instance().size()` 等于**去重后**的期望条目数（而非重复累加值）。
  - `test_gallery_sync_shard_index_out_of_range`：发送 `shard_index = 5`、`shard_total = 2`，
    断言不改变聚合状态（`is_complete()` 仍为 false，`size()` 不变）。
  - ⚠️ 直接测 `ShardedSyncContext` + 解析器即可（该测试目标已 include
    `src/algorithm/runner`），不必起真实 runner 进程。
- [ ] **6.5** 在 `main()` 中按既有风格注册新增用例。

**验证项**

```bash
python3 native/scripts/build.py test
```

- 新增用例 PASS，既有用例零回归。

---

## 阶段 7：全量回归与规范更新

- [ ] **7.1** `python3 native/scripts/build.py test`（全量 ctest，含 FrameBroker / IpcSocketFd /
      AlgoRunnerGallery / AlgoRunner / AlgoRuntime）。
- [ ] **7.2** `go test ./internal/algorithm -run TestIpcContract -v`（IPC 常量未漂移）。
      ⚠️ 若该包间接依赖 `internal/engine`，改用 `make go-check`。
- [ ] **7.3** `make check`（前端 lint/type-check/test + native-test + api-docs + go-check）。
- [ ] **7.4** 更新 `.trellis/spec/native/algo-plugin-contract.md`：
  - §6.4 补充：`FrameDispatchMsg.reserved_fd` 恒为 `-1`，真实 DMA-BUF 句柄**只**经
    `SCM_RIGHTS` 传递；接收端**只认本地副本 fd**。
  - §6.3 补充：槽位容量不足时 `publish_frame` **显式失败**（返回 false、记账、
    归还配额），**严禁**发出 `MSG_FRAME_DISPATCH` 而槽位仍是旧像素。
  - §6.4 补充：发送路径 `EAGAIN` 必须有界 `poll`，杜绝在 `SO_SNDTIMEO` 下自旋
    （调用链含持 `Engine::control` 的广播路径）。
- [ ] **7.5** 更新 `.trellis/spec/native/pipeline-guidelines.md` §5：
  补充「发布失败是显式失败，不是静默丢帧」与「慢消费者导致发送超时后走租约清算」的契约。

**验证项**

- `make check` 全绿。
- 规范文档的改动能被下一个读它的人用来判断「这段代码是否写对」。

---

## 提交前检查（对应项目硬性约定）

- [ ] 无 `_ = fn()` 式吞错。
- [ ] 无 `panic` / 未捕获异常进入业务路径。
- [ ] 未把 `context.Context` 存进结构体字段（本次无 Go 改动）。
- [ ] 无全局可变业务状态（`fail_publish` 是成员函数，`frame_payload_bytes` 是无状态纯函数）。
- [ ] 测试与源码同目录规范：新增用例放在既有 `native/tests/*_test.cpp` 中，符合项目约定。
- [ ] 每行改动可追溯到 PRD 的 REQ 编号。

---

## 回滚点

| 阶段 | 回滚方式 |
| --- | --- |
| 1–2 | 独立文件，`git checkout -- native/src/algorithm/ipc/unix_socket.cpp ipc_protocol.hpp` |
| 3–4 | `git checkout -- native/src/algorithm/ipc/frame_broker.{hpp,cpp}` |
| 5 | `git checkout -- native/src/algorithm/runner/runner.cpp` |
| 6–7 | `git checkout -- native/tests/` |

无 schema / 协议 / 配置变更，整体 `git revert` 即可完全回滚。
