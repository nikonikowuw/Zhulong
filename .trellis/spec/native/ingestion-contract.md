# 原生视频接入与静态构建合同 (Ingestion Contract)

> `include/Zhulong/engine.h` C ABI 门面、RTSP 接入生命周期、CGO 超时解耦、静态 FFmpeg 归档与跨平台构建契约。
> 当修改引擎 ABI、拉流生命周期、静态 FFmpeg 版本、CMake 构建脚本、CGO 链接配置或交叉编译 Profile 时执行本合同。

---

## 1. 导出 C ABI 签名概览 (`engine.h`)

所有符号均在唯一公开头文件 `native/include/Zhulong/engine.h` 中以纯 C 导出：

- **引擎生命周期**：`Zhulong_engine_create`、`start`、`stop`、`destroy`
- **流探测 (Probe)**：`Zhulong_engine_probe`、`Zhulong_probe_result_view`、`Zhulong_probe_result_destroy`
- **流会话生命周期**：`Zhulong_stream_acquire`、`release`、`get_status`
- **帧包订阅通道**：`Zhulong_stream_subscribe`、`unsubscribe`
- **构建入口**：`make native-deps`（联网拉取固定依赖）、`make native-build`、`make native-test`、`make go-check`
- **交叉编译**：`make native-cross-build CROSS_PROFILE=... CROSS_OUTPUT=build/targets/NAME/Zhulong`

---

## 2. 核心契约与安全铁律

### 3.1 默认参数与超时基准

- `options == NULL` 或超时填 0 时：默认使用 TCP 传输、5000ms 打开超时、5000ms 空闲超时。
- 打开流与读取媒体信息共享单调递增的绝对截止时间。即使 FFmpeg 成功返回元信息，也必须二次检查截止时间与取消标记，防止中断后返回残缺元数据。

### 3.2 ⚠️ Go Context 取消与 Native 超时解耦（防时序竞争）

在 Go 侧 `internal/engine` 中，根据调用形态对超时执行差异化处理：

1. **`Probe`（异步可取消）**：
   - 依赖 `select { case <-ctx.Done(): ... }` 进行异步取消，**严禁将 `ctx` 截止时间折叠进原生 open timeout**；
   - 折叠会导致浮点毫秒截断（如 199.7ms 截为 199ms），使原生底层先于 Go ctx 超时报错，返回原生 `TIMEOUT` 错误码而非 `context.DeadlineExceeded`，引发调度竞争故障；
   - 原生 timeout 仅作为最底层的后备防爆兜底，`ctx` 拥有绝对取消权威。
2. **`Acquire`（同步阻塞 CGO 调用）**：
   - 属于同步调用，**必须将 `ctx` 截止时间折叠进原生超时**，防止底层阻塞调用超出调用方的生命周期预算。

### 3.3 线程归属与销毁时序

- **上下文线程亲和**：仅持有 input 所有权的 Worker 线程允许访问与关闭 `AVFormatContext`。在关闭句柄前必须先触发取消（防止 RTSP TEARDOWN 挂死）。
- **销毁外部串行化**：`Zhulong_engine_destroy` 必须与所有其他 API 外部串行化；严禁在订阅包回调内部调用任何控制、探测或销毁接口。
- **异常隔离与自愈**：订阅通道回调中抛出 C++ 异常时，引擎必须逐订阅者捕获隔离，仅将违约订阅者置为静默失效，绝不中断同流其他订阅者，也不得将流标记为 FAILED。

### 3.4 流去重与 8 秒宽限期

- **URI 规范化去重**：按规范化 RTSP URI 执行流去重，相同 URI 复用同一底层流；重复的消费者 ID 不增加引用计数。
- **8 秒退出宽限期**：当某流所有消费者全部 release（引用归零）后，启动 8 秒宽限期定时器；若 8 秒内有新请求重新 acquire，立即取消定时器并无缝复用物理连接；宽限期满后才真正关闭网络流与释放连接。

### 3.5 内存所有权与 CGO 指针合规

- **包视图即时有效**：回调传递的 `Zhulong_packet_view` 仅在回调同步执行期间有效，下游需要保留数据必须深拷贝；
- **禁止传递 Go 指针**：回调令牌 `token` 必须通过 `uintptr_t`（`cgo.Handle` 转换的整数）传递，严禁向 C 暴露 Go 堆指针；Go 侧必须在 `unsubscribe` 彻底排空（drain）并确认无在途回调后，方可注销句柄。

### 3.6 静态构建与跨平台链接门禁

- **FFmpeg 静态绑定**：全仓使用固定的 FFmpeg 7.1.5（SHA-256: `de668509caf9e35e3cd162473441fdb29538c6d96ed080292b3cf9e6fc5d558f`），构建期严禁动态联网下载，绝不回退到系统共享库。
- **内容寻址归档**：使用基于内容摘要命名引擎静态归档，使 C++ 源码改动能准确使 Go 侧 CGO 缓存失效，杜绝陈旧库静默复用。
- **⚠️ 交叉编译强制链接 `Threads::Threads`**：宿主（glibc ≥ 2.34）已将 pthread 合并至 libc，容易掩盖缺失依赖；而目标板（如 RK3568 Debian 11 / glibc 2.31）仍为独立 `libpthread`。所有使用 `std::thread` 的测试或库必须显式链接 `Threads::Threads`，否则交叉编译报 `undefined reference to pthread_create`。

---

## 3. 常见状态与校验矩阵

| 条件 | 预期行为 / 返回码 |
| :--- | :--- |
| 空句柄、非法 URI 或无效参数 | `Zhulong_ERR_INVALID_ARGUMENT`；清空输出句柄 |
| 引擎尚未启动或已停止 | `Zhulong_ERR_NOT_RUNNING` |
| 重复添加相同消费者或订阅者 | `Zhulong_ERR_DUPLICATE`；不增加引用计数 |
| 相同 URI 传入不同传输协议或超时参数 | `Zhulong_ERR_CONFIG_CONFLICT` |
| 存在活跃订阅时直接调用流释放 | `Zhulong_ERR_BUSY`；必须先 unsubscribe 后再 release |
| 未命中消费者、流或订阅句柄 | `Zhulong_ERR_NOT_FOUND` |
| 打开、读取或流信息解析超时 | `Zhulong_ERR_TIMEOUT`；仅暴露脱敏错误码，禁止外泄 URL 凭据 |
| 回调内部尝试重入调用控制类 ABI | `Zhulong_ERR_CALLBACK_CONTEXT`；阻断重入死锁 |

---

## 4. 质量验证与陈旧产物防范

```bash
# 执行完整 15 项 Native CTest 回归
make native-test
```

### ⚠️ 防范陈旧产物伪绿 (Stale-artifact False Green)

CMake 仅根据修改时间增量编译。当改动 `native/src/**`（尤其是 `src/abi/engine.cpp`、公开头文件或 C ABI 函数）时，为杜绝旧 `.a` 伪绿，必须执行完整的三步验证：

1. **彻底清理旧构建**：`rm -rf build/native/host`；
2. **运行全量测试**：`make native-test`（断言 100% 测试通过）；
3. **符号存在性核验**：

   ```bash
   nm build/native/host/libZhulongEngine.a | grep -c '<新增的导出符号>'
   ```

   若输出为 `0` 说明代码未打入静态库，属于陈旧产物伪绿！导出的 C ABI 函数应具备 `T`（定义）与 `.cold`（异常分支）条目。
