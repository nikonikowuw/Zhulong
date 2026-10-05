# Go/CGO 媒体桥接设计（已批准）

用户于 2026-10-05 在规划摘要后回复“开始实现”；以下设计已获批准，API 在实现前仍为拟定契约，不是既有能力。

## 1. 边界与数据流
本子任务只交付桥接，不交付摄像机 API 或浏览器播放。保留 FFmpeg 7.1.5、Native C++17、现有公共 ABI 和静态链接构建入口。

```text
后续 camera / StreamHub（纯 Go）
        ↓ Probe / Acquire / Status / Subscribe / Next
internal/engine（生命周期、token、Go-owned 队列）
        ↓ C ABI 控制调用       ↑ 内部同步 C→Go 回调
Native 物理流池 + 静态 FFmpeg
```

依赖方向：业务 → engine；engine 不引用 camera/app/HTTP/数据库。C/unsafe/cgo 留在 engine 包内部。New 无副作用，Start 保持显式启动。

## 2. Go 门面（拟定签名，尚未实现）

```go
func New() *Engine
func (e *Engine) Start() error
func (e *Engine) Stop() error
func (e *Engine) Ready() bool
func (e *Engine) Close() error

func (e *Engine) Probe(ctx context.Context, uri string, options StreamOptions) (VideoInfo, error)
func (e *Engine) Acquire(ctx context.Context, uri string, consumerID uint64, kind ConsumerKind, options StreamOptions) (*Stream, error)
func (s *Stream) Status(ctx context.Context) (StreamStatus, error)
func (s *Stream) Subscribe(ctx context.Context, options SubscriptionOptions) (*Subscription, error)
func (s *Stream) Close() error
func (s *Subscription) Next(ctx context.Context) (Packet, error)
func (s *Subscription) Done() <-chan struct{}
func (s *Subscription) Err() error
func (s *Subscription) Close() error
```

- Stream 表示一次消费者持有，不是公开 C stream_id；内部绑定 Engine generation、stream ID、consumer ID/kind。同一物理流多个消费者各有独立 wrapper。
- Acquire/Status 的 ctx 控制入场等待，Acquire 成功后生命期由 Stream.Close/Engine.Stop 管理；不能把短 HTTP request ctx 隐式用作长期流生命期。
- Subscribe 的 ctx 控制订阅生命期，取消会通知非回调清理路径；Next 的 ctx 只取消本次读取等待。Close 同步等待安全清理，不提供“返回了但 C 仍在回调”的伪超时。
- 一个 Subscription 单接收者，后续 Hub 自行广播；不暴露任意用户 callback，避免回调重入/阻塞无法约束。
- VideoInfo 使用 Codec、Width/Height、Rational FPS/TimeBase、Go-owned ExtraData。Packet 使用 Go-owned Data、int64 PTS/DTS、HasPTS/HasDTS、Rational TimeBase、KeyFrame。
- StreamOptions 用具名 Transport 与 time.Duration 的 OpenTimeout / IdleTimeout；0 沿用 5s 默认。拒绝负数、毫秒换算溢出和非法枚举。Deadline 比 options 更早时采用剩余预算；不能四舍五入为 0 而回落默认 5s。
- URI 必须已是合法百分号编码 RTSP URI，拒绝嵌入 NUL；不在桥接处理裸凭据歧义，不打印输入。

## 3. 所有权契约

| 对象 | 分配者 | 释放者 / 有效期 |
| --- | --- | --- |
| 常驻 Native engine | C engine_create | Go owner 调用 C engine_destroy，仅在所有操作退出后 |
| 探测用私有 Native engine | C engine_create | 本次 probe owner stop/join 后 destroy |
| URI C 字符串 | Go 调用 C.CString | 同一次方法中的 defer C.free；Native acquire 返回前已复制 |
| probe result / view | Native | view/extradata 借用到 result_destroy；Go 先完整复制再销毁 result |
| packet view / payload | Native worker | 只在回调中借用；Go 不 free，不带出回调 |
| uintptr_t token | Go cgo.NewHandle | 成功 unsubscribe/drain 或确认 Stop 已 drain 后恰好一次 Delete |
| 入队 Packet.Data | Go make/复制 | 单接收者获得所有权；GC 回收，不指向 Native 缓冲 |

### 内部回调
- 使用独立 C shim 适配 `const Zhulong_packet_view*` 到 `//export` Go 函数；定义放 `.c`，export 文件 preamble 仅声明，避免重复符号。只包含公开 Native 头文件及标准 C 头，不暴露 C++。
- C 仅保存整数 token，不保存 Go 指针。注册先建立完整 Go state/token，再调用 subscribe；允许 C 在 subscribe 返回前调用，回调不得依赖尚未写回的 subscription ID。
- callback 校验 nil、size_t→int 转换和大小上限，使用 unsafe.Slice 临时借用，复制一次进 Go-owned 缓冲。零长度按契约处理，非法指针/长度组合转安全终态。
- callback 不做网络/日志/数据库 IO、不调 Native 控制接口、不等待消费者、不启动逐包 goroutine。允许短时内部互斥以维护队列预算，禁止持有业务或 Engine 生命周期锁。
- 防止 panic 越过 C 边界：受控 recover 仅用于边界保护，置固定内部错误并通知清理；不输出 panic value/原始数据，不把失效 token 静默当作成功。OOM 等 runtime fatal 不承诺可恢复。

## 4. 有界缓存与慢消费者
采用内部小队列 + Next(ctx)，不直接暴露接收 channel：这样可以在 dequeue 时准确扣减字节预算并在关闭时释放积压引用。

拟定默认：最多 32 包、单包 4 MiB、队列总载荷 16 MiB；配置字段 MaxPackets / MaxPacketBytes / MaxBufferedBytes。硬上限分别为 256 包 / 16 MiB / 64 MiB；非法配置拒绝，不悄悄扩容。上述是初始工程限制，不是性能实测值，单个合法大包超过限制要明确报错而非截断。探测 ExtraData 的 Go 复制上限 1 MiB，超过则返回明确资源限制错误。

- 先检查/预留预算，再复制；队列满或字节预算不足时标记 `ErrBackpressure`，超大包标记 `ErrPacketTooLarge`，停止接收该订阅后续包并唤醒其清理协程。
- 不用 DropOldest 任意丢 P/B 包后继续装作健康压缩流；终止的订阅由上层重新建立并重新同步参数集/关键帧。
- 一个订阅最多一个控制协程，响应 ctx/Close/过载/Native 失败；可在订阅活跃时每 250ms 查询一次状态，无订阅时不存在该轮询。状态调用不在 callback 内。
- 关闭清空队列，等待中的 Next 被唤醒；终态发布先于 Done 关闭。正常 Close 的 Err 为 nil，Next 得到可识别的关闭错误；故障保留首个终态原因。
- 这些预算只约束桥接保有的数据，不能约束上层已通过 Next 取走后自行无限保留的数据。后续 Hub 仍必须做总连接/总字节限制。
- 复制一次是跨异步边界的必要取舍；后续按物理流复用一个 Hub 订阅，可在 Go 侧共享只读 Packet，避免每个浏览器都跨 C 重复复制。不是“端到端零拷贝”。

## 5. 生命周期、锁与并发顺序

### 常驻 Engine
- 现有单个 mutex 跨整个 C 调用的方式不能直接扩展到 probe/drain。分离生命周期转换串行化、短时状态/准入保护、在途操作记录与订阅队列锁。
- 新操作在状态锁内验证 running/generation，并登记在途操作；一旦 stopping，不再允许新入场。Stop/Close 不能等待一个尚未请求取消的 probe。
- Stop/Close 标记 stopping → 关闭新操作准入 → 请求私有 probe 取消 → 调用常驻 Native stop 取消 worker 并 drain → 等待已入场控制调用/私有 probe 与订阅清理 → 回收 token/Go 队列 → Stop 保留 handle / Close destroy。
- 回调/清理协程要使用的锁不能跨 C.stop、unsubscribe、等待操作计数或协程 join 持有；并发 Stop/Close 等待同一个转换完成，不重复 destroy。
- 销毁前必须证明全部外部 C 操作结束。Start 要等前一轮清理完成，再进入新 generation；旧 Stream / Subscription 不能拿复用的数字 ID 操作新代资源。
- Ready 只读取 Go 状态，不等长 C 操作；stopping 时为 false。保留 Stop-before-Start、幂等调用、Close 后可再 Start。

### Stream / Subscription
- 一个消费者一次 acquire，Stream.Close 先关闭其订阅再 release。重复 Close 复用一次清理结果。关闭某一消费者不影响同流其他消费者。
- subscribe 失败时尚未发布的 token 立即回收；成功路径发布需要与 Stop/取消竞争同步，避免成功返回却遗漏注册记录。
- 正常退订：禁止本地继续入队 → C.unsubscribe 成功 drain → Delete token → 清空队列/关闭 Done。仅发生 NOT_FOUND/NOT_RUNNING 不能盲目认为已 drain；须由同一 generation 的全局 Stop 完成证据兜底。
- Stream.Close/Subscription.Close/自动清理/Engine.Stop 共享一次性清理所有权。失败且没有 drain 证据时保留 token 和待清理记录、返回安全错误，不抢先 free；随后 Engine.Close 必须兜底。
- 清理协程不等待自己的 Done；callback 只发信号，绝不同步 Close/stop。接收方异步处理，不存在用户函数在 Native callback 栈里重入控制 API。

## 6. 独立 probe 的取消
现有 ABI 只提供整引擎 stop，不能用常驻 Engine.Stop 响应一个请求取消。

最小无 ABI 改动方案：每个 Probe 使用私有 Native engine + 独立网络连接，归属 Go Engine 的在途 probe 集合；最多并发 4 个，名额等待监听 ctx 与引擎停止信号。

1. 准入检查与剩余预算校验；私有 engine create/start 完成后登记，之后才允许 stop。引擎停止或 ctx 在 start 前已取消则不启动。
2. 有界工作协程调用该 engine 的 C.probe；请求方监听结果/ctx/Engine stop。取消只调用私有 C.stop，probe 尚未注册时允许得到 NOT_RUNNING，映射为正确的取消原因。
3. 禁止取消后重新 start；无论谁赢竞争，都等 probe 调用返回，销毁 result、stop/destroy 私有 engine、移除登记和归还名额，再返回。
4. 复制参数集期间仍保持 result 存活。取消与成功同时到达时，返回前再次检查 ctx；被取消的成功结果不得逃逸为业务入库依据。
5. 绝不通过 return ctx.Err() 留下不再 join 的 C 调用。系统 DNS 仍可能拖延取消；文档明确这是协作式取消而不是任意系统调用的硬实时抢占。

备选方案比较：

| 方案 | 收益 | 代价 / 结论 |
| --- | --- | --- |
| 私有 probe engine（拟采用） | 不改 C ABI；取消隔离；复用已测 stop | 每次 probe 额外生命周期/reaper，限制并发 |
| Native 新增单 probe cancel | 更轻量 | 扩大 Native ABI 与测试面；需另行审阅 |
| goroutine 提前返回、C 自行结束 | 表面响应快 | 孤儿操作/销毁竞态；拒绝 |

## 7. 错误与安全
- 公开 Go `StatusCode` / `NativeError` 保留固定状态与操作枚举，支持 errors.As；本地 Closed/Stale/Backpressure/资源上限/参数错误与 ctx 错误可识别。未知 Native 数字保留但不杜撰含义。
- 底层 engine 不生产 HTTP code、i18n 或 camera 领域错误。后续业务层负责映射。
- 不持久化或输出原 URI、userinfo、packet 内容；错误文本与测试日志都遵守固定安全文本。测试用合成凭据覆盖 stdout/stderr。
- 入参校验先于 C 转换，包括 NUL、transport/kind/consumerID、duration/size 溢出。

## 8. 测试与兼容
- 复用 Python RTSP Server；新增测试 runner 用环境变量交给 Go 集成测试并验证连接计数，支持 CMake/Go 包测试共用 fixture 实现但不改既有 CTest 语义。
- 单元测试用于异常 size/元数据、队列上限、generation、token 账本/失败回滚；真实 C→Go callback 是必测项，不以 mock 代替 drain 证据。
- 重点竞态：回调早于 subscribe 返回、close 与 callback、订阅失败、stop 与 subscribe/probe、并发重复 close、两个探测之一取消而另一个及预览不受影响。
- 不把 Go race 当作 Native race 覆盖；C++ 产品代码改动需先审阅范围，再做独立 ASan/UBSan/TSan。
- 现有静态链接脚本、host/cross 隔离、C ABI 原错误码、缓存规则不改。测试运行器不能在宿主执行交叉产物。

## 9. 发布与回退
宿主测试通过后更新 engine 的契约与用法；在摄像机业务开始使用前不接入生产路由。若桥接不达标，可仅撤回新增 Go wrapper/shim/test 与相关文档，恢复旧生命周期实现，不回退 Native 已有功能或 FFmpeg 依赖。

摄像机/Hub 接入后撤回必须按反向依赖顺序处理。交叉编译/板端未验证仍归旧 Native 子任务；本子任务完成不等于父媒体任务完成。
