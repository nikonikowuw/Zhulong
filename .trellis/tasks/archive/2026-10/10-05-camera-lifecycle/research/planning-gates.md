# 摄像机生命周期：剩余规划门禁证据

## 1. 结论与范围

本轮按指定任务 `.trellis/tasks/10-05-camera-lifecycle` 完成两项有界研究：固定 Go 模块的标准下载/校验尝试，以及 design §13/§16 对现有 Native/Go 生命周期的源码对照。`task.py current --source` 仍返回 `Current task: (none); Source: none`；本轮不改变任务指针或状态。

**结论：**

1. 用户已批准 design §15 的 icholy 低层 API、项目参数规范化与固定空 opaque 补充路线。该路线不再以等待未知上游版本为启动门禁；本轮没有重开候选研究。
2. 两个固定模块各执行一次隔离的 `go mod download -json`，均在 proxy.golang.org `.info` 请求阶段连接超时，exit 1；**没有取得可确认的 Sum/GoModSum**。来源校验门禁仍未关闭，但故障定位明确，下一步是在可信可达网络运行相同下载，不需要更换依赖或禁用校验。
3. Native 设计的大方向正确：短期池锁占位、锁外 join/drain、代次订阅、健康证据不依赖包订阅。但目前文字不能直接交给实施：需要明确 **join 独占所有权、Stop 与所有锁外操作的存活期、按消费者追踪 drain、Go 原生句柄借用屏障、线程创建成功后的 generation 提交、sized-output 错误写入边界以及旧 ABI 的新增 BUSY 行为**。
4. 下文给出有限的设计修订清单和针对性验收，不要求无限追加研究。本次是**规划源码复核**，不是运行测试、竞态复现、安全审计或独立最终验收。

项目内仅新增本文件。未实现/修改认证或任何生产代码，未更改旧研究、design/implement、go.mod/go.sum，未 start、暂存或提交。`engine.hpp` 保持指定 SHA-256：`dad479e08adc077e9c5c484c8ebcd87964c4eb6683d669cb838944dc21256148`。

## 2. 固定模块标准来源/校验尝试

### 2.1 隔离方式与命令

工具链实际输出：`go version go1.27.1 darwin/arm64`。临时证据目录：`/tmp/zhulong-planning-gates-a31tru0e`。

每个模块有独立的临时工作目录和仅含以下声明的临时 go.mod：模块名 `example.invalid/zhulong-planning-review`，Go directive `1.23.0`。两个下载共享本轮新建、隔离的临时 GOMODCACHE，但不共享主模块 go.sum。GOCACHE/GOPATH 也指向本轮临时目录。

只对子进程设置以下**公开配置**，没有读取/打印私有环境变量值，没有执行 `go env -w` 或修改全局网络配置：

- `GOENV=off`、`GOWORK=off`、`GOTOOLCHAIN=local`；不读取项目工作区，也不自动下载新工具链。
- `GOMODCACHE=<temp>/gomodcache`、`GOCACHE=<temp>/gocache`、`GOPATH=<temp>/gopath`。
- `GOPROXY=https://proxy.golang.org,direct`、`GOSUMDB=sum.golang.org`。
- `GOPRIVATE=`、`GONOPROXY=`、`GONOSUMDB=`：两个模块均走公开来源/校验，未让私有模块例外绕过校验。
- `GIT_TERMINAL_PROMPT=0`，禁止交互等待。

下载命令各一次，最多两个并发子进程，每个外层硬上限 60 秒；若超时则终止进程组。本轮实际是 Go 自身约 30 秒报错退出，未触发外层 kill。

```text
go mod download -json github.com/icholy/digest@v1.2.0
go mod download -json github.com/bluenviron/gortsplib/v4@v4.16.2
```

只下载，没有 build/test/run，没有执行依赖代码，没有重复访问 release、源码或其它候选端点。使用逗号形式的 proxy fallback 不会因本次网络超时自动切 direct；本轮没有改成绕过该结果的另一套端点尝试。

### 2.2 实际输出与校验状态

| 模块 | 耗时 | 退出 | Sum | GoModSum |
| --- | --- | --- | --- | --- |
| github.com/icholy/digest@v1.2.0 | 30.06 秒 | 1 | 未返回，不能填写/宣称已验证 | 未返回，不能填写/宣称已验证 |
| github.com/bluenviron/gortsplib/v4@v4.16.2 | 30.06 秒 | 1 | 未返回，不能填写/宣称已验证 | 未返回，不能填写/宣称已验证 |

两个进程 stderr 均为空；错误位于 `go mod download -json` 的 stdout.Error。原样记录：

```json
{
  "Path": "github.com/icholy/digest",
  "Version": "v1.2.0",
  "Error": "github.com/icholy/digest@v1.2.0: Get \"https://proxy.golang.org/github.com/icholy/digest/@v/v1.2.0.info\": dial tcp 142.251.46.81:443: i/o timeout"
}
```

```json
{
  "Path": "github.com/bluenviron/gortsplib/v4",
  "Version": "v4.16.2",
  "Error": "github.com/bluenviron/gortsplib/v4@v4.16.2: Get \"https://proxy.golang.org/github.com/bluenviron/gortsplib/v4/@v/v4.16.2.info\": dial tcp 142.251.46.81:443: i/o timeout"
}
```

**证据边界：** 此前直接访问 proxy.golang.org 因网络超时未取得 Sum；随后在宿主已配置的可信代理环境（`GOPROXY=https://goproxy.cn,direct`）中以临时隔离模块与干净缓存复测，成功取得官方 checksum：

- `github.com/icholy/digest@v1.2.0`:
  - Sum: `h1:oTbG4IsNOmidJ+421ehG7Ty93yt1yotq13kFMG569yw=`
  - GoModSum: `h1:1P1+LzUv48ybX7bu8tVpZ2QWdd+xRuePNuGawHjwRUE=`
- `github.com/bluenviron/gortsplib/v4@v4.16.2`:
  - Sum: `h1:10HaMsorjW13gscLp3R7Oj41ck2i1EHIUYCNWD2wpkI=`
  - GoModSum: `h1:Vm07yUMys9XKnuZJLfTT8zluAN2n9ZOtz40Xb8RKh+8=`

来源校验门禁至此已具备确切的 Sum / GoModSum 记录，消除了依赖来源悬空风险。

**有限闭环：** 校验记录已闭环，下一步纳入实施阶段 go.mod/go.sum 统一 pin，无需在规划期修改主模块文件。不以 `GOSUMDB=off` 或改用 @latest 关闭门禁。

## 3. 本次 Native 审阅依据

### 3.1 已加载技能与适用原则

已读取：

- `/Users/niko/.pi/agent/skills/cpp-coding-standards/SKILL.md`（完整文件）。重点应用 RAII、异常安全、CP.2 数据竞争、CP.20 锁 RAII、CP.22 不持锁调用未知代码、CP.42 条件等待与 CP.26 不 detach 线程。
- `/Users/niko/.agents/skills/ffmpeg-streaming/SKILL.md`。
- 同 skill 的 `references/cpp-gotchas.md`、`references/ffmpeg-api.md`、`references/validation.md`：取消状态/interrupt opaque 生命周期、只有输入拥有线程关闭 AVFormatContext、packet 借用/复制、先停止生产再排空/join、测试证据边界。

`.trellis/spec/native/ingestion-contract.md:61–65` 固定 FFmpeg 7.1.5 与源码 SHA-256；本轮只读该合同和 capture 源码，未运行 FFmpeg 二进制/协议能力检查。用户限定下载/源码研究，因此技能中的 ffmpeg/probe/sanitizer 执行步骤留给实施验收。没有 ZLMediaKit 集成，不凭该技能引入新的媒体服务。

### 3.2 源码地图（行号为本轮工作树）

| 路径/行号 | 核查内容 |
| --- | --- |
| native/src/pipeline/engine.hpp:45–171 | Subscription active/enabled；Stream worker/cancel/status；Engine 锁、池、Reaper、Probe 持有关系；只读，含用户既存修改 |
| native/src/pipeline/engine.cpp:30–65 | callback 在锁外调用，disable_and_drain 等待 active=false |
| 同文件:75–137 | Stream 析构/start/join、快照派发、capture 最终 FAILED |
| 同文件:148–247 | start/stop、Reaper 选择/锁外 join/删除池条目 |
| 同文件:250–385 | acquire/release/status/subscribe/unsubscribe |
| 同文件:387–408 | Probe 注册与 RAII 注销 |
| native/src/abi/engine.cpp:38–50,66–96,102–181 | 回调上下文拒绝、异常映射、输出初始化、destroy |
| native/include/Zhulong/engine.h:190–209,239–294 | Stop/Destroy、Release/Unsubscribe 合同 |
| native/src/nodes/capture/rtsp_input.hpp:79–95 | stopped 原子、deadline 仅输入拥有线程访问 |
| native/src/nodes/capture/rtsp_input.cpp:40–65,286–304,340–378 | interrupt/AVFormatContext RAII、open deadline、ready 与有效视频包派发 |
| internal/engine/engine.go:123–205,247–309,326–458 | Stop/Close、私有 Probe、原生句柄快照、token 清理 |
| internal/engine/stream.go:32–40,49–111,114–138 | Go Stream 生命周期与订阅发布/关闭 |
| internal/engine/subscription.go:43–50,118–124,159–176,201–280 | token、逻辑 Done、Close、队列与控制循环 |
| .trellis/spec/native/ingestion-contract.md:30–54,85–105 | 已存在的并发、销毁外部串行化、订阅/宽限期、错误合同 |

## 4. 源码支持的设计复核与最小修订

### N1 — join 所有权应覆盖 restart/Reaper/Stop，不能把 shared_ptr 或 FAILED 当成可并发 join 的许可

**事实：** `Stream::join`（engine.cpp:90–93）直接读写同一个 std::thread，没有内部串行锁；析构（75–77）也会调用 join。Reaper 在 control 下选择流（224–233），锁外 join（237–238），最后删除（240–245）。现有 Stop 先停止/等待整个 Reaper，再交换流池并 join（169–201），因此当前 Stop/Reaper 所有权有实际串行顺序。现有 acquire 在“无消费者且已过期”时等待删除（273–279），与 Reaper 使用同一个过期条件；**不能把缺少 retiring 字段直接宣称为当前已复现的复活竞争**。

**设计缺口：** §16 的占位方向正确，但新增 restart 是第三个 worker 句柄操作方；若只写“shared_ptr 保持活着”或“FAILED 说明已结束”而没有唯一 join owner，仍可能重复 join/在 joinable 的 std::thread 上赋新线程。

**最小修订：**

- 明确 `restarting` 与 `retiring` 为 control 保护的互斥所有权标记，只有取得占位者可操作该流的 worker/join。FAILED 只是可以申请 restart 的状态，不授予 join 所有权。
- Reaper 选择流和写 retiring 必须在同一次 control 临界区；跳过 restarting，按 N2 跳过未完成 drain。占位后保留池记录到 join 完成，acquire 不能取消已经交给 Reaper 的退休。
- restart 持有强引用并保持占位直至旧 worker join 完成、提交/回滚与计数清理结束。不要持 control/stream mutex join，也不要为 join 引入 worker/Stop 反向获取的第二个长时锁。
- Stop 在 control 下关闭准入、设置取消；等待在途 restart（等待释放 control），再 join Reaper，再接管剩余流；不得在 restart 仍拥有旧 worker 时自己 join 它。析构只作已无并发所有者时的兜底，不能承担竞争仲裁。
- restart 提交必须重新检查**发起该请求的 consumer_id 仍存在**，不只是 `!consumers.empty()`；否则 A 释放后 B 仍在会让已失效的 A 请求继续启动。若允许同 ID 在此窗口重新 acquire，还要有消费者租约代次；最小方案是 restarting 期间同源 acquire 仍返回 BUSY，防止此 ABA。

**闭环测试：** 控制点暂停在“已占位未 join / join 后未提交”，并发 Stop、Reaper、最后引用释放；断言同 worker 的有效 join 只有一个所有者、另一路源不被全局锁阻塞。

### N2 — drain 要同时按 stream 与 consumer 记账；release 不能只看 subscriptions map

**事实：** unsubscribe 在 engine.cpp:372–377 从 subscriptions 删除记录，381 解锁，384 才执行 drain。release 只检查仍在 map 中的订阅（318–326）；因此并发 release 可以在该消费者的 unsubscribe 尚未完成时成功，开始宽限期。当前单生产者加 shared_ptr 可以让已开始的 unsubscribe 保持订阅对象活着，但**不证明引用/重启准入已经满足新合同**。

**设计矛盾：** §13.2 要求旧订阅完全排空；§16:197 仅写了一个不明确归属的 draining 计数，未说明 release/重复 subscribe/消费者消失后的记账规则。只有“restart 看全流 draining_count”不足以执行“release 前 unsubscribe 完成”的合同。

**最小修订：**

- 记录 stream 级 `draining_total` 与对应 consumer 级 draining 数（或等价保留的 draining 订阅记录）。在删除 map 记录之前/同临界区完成可失败的记账准备；不能 erase 之后因分配失败遗失 drain 责任。
- 活跃 map 记录与 draining 记录都算该消费者仍有订阅：release 返回 BUSY，消费者不得被删，宽限期不得开始。同 consumer 的新 subscribe 也不能绕过 drain；全流 restart/new-generation subscribe 要求 draining_total=0。
- unsubscribe 的本地强引用、drain 记录与注销守卫一起活到 disable_and_drain 完成及最终记账结束；先释放 Subscription/stream mutex，再按 control 顺序注销，不能持 subscription mutex 反向获取 control。
- Reaper 只处理消费者为空且 drain 已完成的流；采用上述 release BUSY 后该条件通常自然成立，但显式不变量更易验收。
- restart 期间 release 仍可释放**没有活跃/排空中订阅的**消费者，让 restart 在提交时取消；不要把这句话误读为 drain 未完成也能 release。

**闭环测试：** 在 unsubscribe 删除 map 后暂停，release 同 consumer 必须 BUSY、其它 consumer 合法 release 不受阻、同 consumer 订阅不能新建、restart 不得越过 drain；放行后各操作可继续。保留现有“第一回调阻塞时可独立排空第二订阅”的快照语义（rtsp_integration_test.cpp:349–386），不能改成全流 join 才允许任意 unsubscribe。

### N3 — Native Stop 的计数不能只覆盖 restart；锁外 unsubscribe 的完成路径也会再访问 Engine

**事实：** 现有 Stop（engine.cpp:165–207）等待 worker、Reaper、注册的 Probe；没有控制调用完成计数。现有 unsubscribe 的锁外阶段只访问本地强引用，**拟议 §16:197 则新增了 drain 后重新取得 Engine::control 的步骤**。Probe 已展示可参考的注册/RAII 注销形式（387–405），但其计数不能自动覆盖新控制操作。

**设计矛盾/危险时序：** unsubscribe 完成 callback drain 后被调度暂停；Stop 只看 restart 计数和 worker 已 join 就返回；Destroy 释放 Engine；unsubscribe 恢复后按新设计访问 control/draining bookkeeping。这是新完成路径的对象存活要求，不是“所有 callback 都退出了”就能解决。

**最小修订：**

- §16 明确 Native 已准入的 restart **和会解锁后再次访问 Engine 的 unsubscribe、条件变量等待中的 acquire 等控制操作**都纳入存活期注册，计数/注销由 RAII 管理。drain 结束时即使 running=false，也必须允许完成注销，不能再经过会抛 NOT_RUNNING 的 find_stream 后跳过减计数。
- Stop 先关闭准入和发出 worker/probe 取消，再等待这些控制完成路径；条件等待释放 control。不能等操作退出之后才通知它们所依赖的取消/退休条件，否则形成关闭死锁。
- 计数清零与通知必须是操作最后一次访问相应 Engine 状态的同步阶段；明确析构顺序和所有异常路径，避免“先 decrement，再访问已可销毁的对象”。
- **Stop 完成不等于允许任意外部线程继续持原始句柄调用。** 保留 engine.h 的 Destroy 必须与所有其它 API 外部串行化约束；尚未进入 Native 注册点、仍停在 ABI 参数处理/Go 调度中的调用由 N4 的调用方 pin 保护。不能声称 Native 内部计数自动修复所有 raw-pointer 销毁竞争。

**闭环测试：** 暂停 unsubscribe 在“drain 已返回、尚未控制记账”，Stop 不得越过新的存活期屏障；并发 stop/restart 异常路径不漏计数。Go C-call 返回后的清理另由 N4 验证。

### N4 — Go wrapper 目前没有句柄借用屏障；必须先补全所有调用存活期，再增加 restart/health

**事实：** engine.go:326–339 的 Acquire、367–377 的 status、393–413 的 subscribe、423–433 的 unsubscribe、444–453 的 release 都在 e.mu 下复制 handle 后解锁，再调用 C；没有在途注册。Close 在 194–203 的 stopInternal 之后直接 destroy。§16:199 说“等待所有已登记调用”，但现代码并不存在这套登记。

**源码可推导的风险时序（未运行复现）：** status/Acquire 获取 handle 后暂停，Close 完成 Stop 和 destroy，原调用恢复后使用旧指针。Acquire 的迟到结果清理（353–357）还可能再次用旧 handle 调 release。引擎 generation 只在调用前后检查不能保护 C 调用期间的地址存活。

私有 Probe 也有独立句柄风险：Stop 从 activeProbes 复制 pCtx 后解锁（134–145），Probe 完成可从 map 删除并 stop/destroy（252、268–273）；复制的 pCtx 指针并不保证它的 C engine 未销毁。不能把 main Engine 的 op count 当作私有句柄已被 pin 的证据。

**最小修订：**

- 在 e.mu 下完成“生命周期状态/代次校验 + 对 handle 的在途借用登记”，与 closing/stopping 准入关闭使用同一同步域。归还借用必须在 C 返回、迟到资源清理、必要 token/订阅发布或回滚之后。覆盖旧 Acquire/status/subscribe/unsubscribe/release 与新 health/restart，不只覆盖新增函数。
- Stop/Close 不持 e.mu、Stream.mu 或回调需要的锁等待 C/join/drain。不要简单给所有调用加生命周期互斥锁并一直拿着进 C：Acquire 等待退休时可能需要 Stop 去取消，长期互斥锁会把销毁竞争换成死锁。
- 明确两阶段停止：先关闭新业务准入并发出取消/唤醒，允许已经登记的调用及内部清理完成；在 Native stop 完成与所有借用归还之后才 destroy/重开下一代。**不得在发送 Native Stop 之前无条件等待所有在途调用**，否则等待中的 acquire/restart 可能无法被取消。若内部 Stream.Close 需发 cleanup 调用，要有明确内部清理通道或放在 Native stop 屏障之后做本地清理，而非被“业务准入已关闭”误拒绝。
- 对私有 Probe context 明确独立的 stop 借用/所有权保护：快照时 pin，完成移除后等待所有快照 stop 借用退出再 destroy；或在可证明不反向锁的短生命周期保护下完成 Stop 调用。不能只 snapshot 裸 C handle。
- context 取消只请求中止；不能让 Go 提前返回并释放仍被 C 使用的 URL/options/output 内存。现有 Probe 取消分支等 resChan 返回（303–309）的做法应保留这种完成屏障。

**闭环测试：** 用测试屏障暂停在“Go 已取得 handle、尚未进入 C”“C 成功但尚未注册/回滚 Go 对象”“私有 Probe snapshot 后、完成 destroy 前”，并发 Close/Stop/Start；检测 UAF、遗漏清理、死锁和旧代次写回。现有 engine_test.go 的并发生命周期测试只覆盖 Start/Stop，并不代替这些窗口。

### N5 — token 删除与 Close 成功不能仅依据 Done、NOT_FOUND 或 NOT_RUNNING

**事实：** engine.go:434–435 在 unsubscribe 返回 OK、NOT_RUNNING、NOT_FOUND 任一种时删除 token；subscription.go 的 Done 可在背压/错误发生时就关闭（118–124、265–274），实际 unsubscribe/drain 由后续控制循环完成。Subscription.Close（202–230）无论 unsubscribe 是否成功都清队列并 onSubClosed；Stream.Close（123–128）忽略 sub.Close 的错误继续 release。Close 错误只是函数局部变量，后续 sync.Once 调用可能返回 nil，并不重新提供 drain 证明。

**设计矛盾：** §13.3:130 的“仅成功同步 drain 后删除 cgo.Handle”与当前 wrapper 的错误码捷径不一致；Stop 已设 running=false 但 worker 仍在退出过程中时，NOT_RUNNING 本身不证明 callback 已排空。重复 unsubscribe 的 NOT_FOUND 也可能只是另一个调用已经移出 map 但仍在 drain。

**最小修订：**

- 明确 `Done/closed` 是逻辑终态，不能代替“Native drain 已完成”的屏障。协调器必须等实际 Close/drain 成功或该 Engine 代次的 Stop-completed 屏障。
- Token 只在以下证据之一成立后删除：本订阅同步 unsubscribe 成功；同一次 unsubscribe 的共享完成结果证明已排空；对应代次 Native Stop 已完成、无未来 callback。NOT_FOUND/NOT_RUNNING 不独立构成证据。
- Close 保留共享完成状态与错误，不能让第一次失败后第二次返回 nil 被当成已排空；非正常错误路径保持可最终清理，不在旧 token 仍可被调用时移除 activeSub/释放其消费者。
- 重审 `TestEngineUnsubscribeNotFoundDeletesToken`（engine_bridge_test.go:167–187）：它只对从未存在的 id 使用普通闭包，证明不了并发 drain 期间真实 cgo.Handle 可安全删除；应增加真实 token/回调屏障用例，而不是保留这个测试就宣布新合同满足。

这是与重启代次直接相关的旧 wrapper 缺口，不建议顺手扩大到无关的队列/播放器重构。

### N6 — capture generation 在线程创建成功后提交；失败不能偷偷消耗新代次

**事实：** `Stream::start`（engine.cpp:80–82）直接构造 std::thread，可能抛异常；对一个仍 joinable 的 worker 赋值还会触发不可恢复错误，因此必须先取得 N1 所有权并 join。初次 acquire 失败时删除新流（295–305），但 restart 要保留原物理流/消费者，不能照搬删除整个实例。

**文字矛盾：** §13.1:109 说“每次实际启动新 worker 增加”，§16:193 的顺序却是“更新代次/证据并启动 worker”；若先公开 G+1 后线程构造失败，仅恢复 FAILED 并不能满足 generation 的定义。

**最小修订（建议明确采用一种提交规则）：**

- 在 control 占位下完成旧 worker join；提交阶段再核验 running、未退休、发起消费者仍有效、expected generation 和 drain 条件。
- 计算候选 G+1，但不先向查询方发布。持 control→stream mutex 的短临界区准备 STARTING/清空媒体证据/复位取消，尝试构造新 worker；成功安装线程句柄后才提交 G+1，并释放锁让新 worker 的 ready/health/status 更新可见。若需要启动门控，可用极小的启动屏障，而不是让新线程发布旧代次证据。
- 构造失败：保留原 stream id 和消费者、generation 仍为 G，恢复 FAILED 和安全错误，取消标志恢复为 stopped，has_video_packet=false，输出 generation 保持失败值 0，清理 restarting/在途计数，后续可用同一个 expected G 重试。不要留下 STARTING，也不要 erase 原流。
- generation 溢出在修改状态/启动线程前拒绝。新 worker 一旦成功创建即使立即握手失败，也属于实际创建的新代次；restart 成功只代表创建启动成功，不代表媒体在线。
- reset cancel 只允许在旧 worker（包括 FFmpeg Input 析构/关闭路径）已 join 后。rtsp_input.cpp:61–64 的 Input 析构会再次写 stopped=true；太早 reset 会被旧析构覆盖或重新允许旧 IO。deadline 由新 read_rtsp:289–291 在使用前初始化；不要由 Go 控制线程与运行 worker 并发写它。

**闭环测试：** 注入 std::thread 创建失败，assert G 不变/输出零/无新连接/状态 FAILED/计数归零；随后同 expected G 可成功得到 G+1。另测“新线程极快成功/失败”不会先发布旧 generation 或被迟到旧 worker 状态覆盖。

### N7 — sized output 的失败清零必须先证明尺寸；不能沿用旧 ABI 的无条件结构赋值

**事实：** 旧 get_status（abi/engine.cpp:128–134）在入参 require/boundary 前写完整结构，初始值是 `{FAILED, INVALID_ARGUMENT}`，**并不是全零**。旧 probe_result_view 则用 `{}`（175–180）。这些旧函数没有 size 参数，默认调用方提供完整声明类型。

**设计缺口：** §13.1:108 说新 health 传调用方输出大小，§13.2:125 又笼统要求输出失败时清零。若直接模仿旧代码，在校验 out_size 前 `*out = {}`，会越界覆盖较小调用方缓冲区；若 memset(out, 0, out_size)，又可能按照不可信长度写超出已知结构。

**最小修订：**

- 为新 health 明确 v1 尺寸合同。最简单可审阅方案：out 非空且 out_size **恰等于**该目标 ABI 的 health_v1 大小才允许任何结构写入；小/大尺寸都 INVALID_ARGUMENT 且不写输出。需要 prefix/version 扩展时另写准确前缀规则，不隐式接受。
- 合法形状的输出先置为定义的失败值/零；只在调用成功时写完整有效快照。**尺寸/指针无效的错误是“不写”的明确例外**，不能用“所有失败清零”覆盖该例外。不得写未知尾部；正确长度仍是 C 调用方对实际可写容量的承诺，Native 无法探测任意虚假指针。
- 结构只用固定宽度标量和明确 reserved 字段；C/C++ 的 sizeof/offsetof 对照与 Go wrapper 使用同一头文件，避免 C++ bool/指针/时钟 epoch。计数、generation、has 与 age 在同一 stream mutex 快照下产生。
- 新 restart/subscribe_generation 的标量输出只在非空、完整合法指针的前提下置 0；C异常/回调上下文拒绝遵守已写清的失败输出合同。
- **旧 get_status 的布局和错误 sentinel 必须保持原样**，不要为了新“全零规则”顺便更改它。

**闭环测试：** 纯 C 短/长/零 size 与 canary、不完整指针范围的安全边界、正确大小各种错误与成功、C++/C 布局断言、Go 值拷贝。仅现有 engine_abi_test.c 的空指针测试不足以证明 sized-output 安全。

### N8 — “旧 ABI 保留”与“默认语义完全不变”需拆开，明确 BUSY/冲突合同

**事实：** 旧 acquire 会在可复用未过期流上直接增加消费者（engine.cpp:282–292），旧 subscribe 只检查消费者/重复订阅，不看 stream state（343–364）。§16:194 新增 restarting 期间 acquire/subscribe BUSY；N2 还要求 release 遇排空中的订阅 BUSY。§13.3:131 的“现有 Status/Acquire/Release 默认语义不变”因此不能字面覆盖全部并发窗口。

**最小修订：**

- 保留旧函数签名、结构布局、既有错误码取值、正常非重启路径及 8 秒失败流宽限期；显式记录新增重启/退休/排空临界窗口里的 BUSY 及重试规则。这是安全收紧的行为差异，不是二进制 ABI 破坏，也不能声称零行为变化。
- 旧 subscribe 与新 generation-aware 入口共享占位/drain 检查，前者没有代次承诺，后者在同一临界区校验 expected generation 与注册。不要用旧入口加一次先查后订阅模拟新合同。
- 明确新 generation mismatch 的稳定错误码与 Go 映射、与参数错误/NOT_RUNNING/BUSY/NOT_FOUND 的优先级；当前 ABI 并不存在专门的 generation conflict 符号，不能让实施者凭“返回冲突”随意复用/重编号旧枚举。可新增专用代码，但需父会话在 ABI 草案中确定。
- 只有协调器管理的受控 restart 改变 FAILED 流；旧 Acquire 不自动重连，不改变 dedup/options 冲突规则。

**闭环测试：** 同一组旧 ABI 用例在正常/重启/排空窗口分别断言结果；新冲突不注册 token、不增加消费者；跨旧/新订阅入口不能绕过占位。纯 C ABI 与 Go errors 映射同批更新。

## 5. 健康证据与测试前提的补充核对

### 已支持设计、无需另造接口语义的部分

- rtsp_input.cpp:359–376 只对选定视频轨道且 size>0 构造/派发 packet；因此在 Stream::capture 的 consume lambda 里、dispatch 前更新单调时间/has 标记，可以不新增逐包 CGO 回调。
- ready 在实际读取 packet 之前调用（340–354），Stream capture 的 ready 仅置 RUNNING（engine.cpp:122–125）。保留“RUNNING 不等于有视频证据”、首包前 has=false 的方向是必要且源码一致的。
- Input 在 owning worker 栈内 RAII 关闭，cancel 为 Stream 成员；restart 等旧 worker join 后重置，才能同时满足 FFmpeg opaque lifetime 与无双连接要求。不得在控制线程直接 avformat_close_input。
- 健康查询不触碰 consumers/expiry、不 acquire 虚拟 Preview 引用；数据只复制固定宽度快照，不传 steady_clock epoch 给 Go 解释为 UTC。

### 一个需要修正文案的测试前提

§13.4:138 写“旧回调阻塞、unsubscribe 已移出 map 但未 drain 完成时，restart 必须 BUSY”。当前 worker 同步调用回调，**只有 read_rtsp 返回/异常展开完成后**才设置 FAILED（engine.cpp:114–137）。真实同一 worker 的 active callback 正被阻塞时，通常还不能同时自然观察到该 worker 已进入 FAILED。因此：

- 可以将“任何 restarting/draining 占位优先返回 BUSY”作为明确 API 优先级，测试该拒绝路径；不要把这个用例误称为已经覆盖“FAILED+active callback”的自然时序。
- 真正覆盖 FAILED 重启与排空计数的测试应暂停在“callback 已结束、旧 worker 已 FAILED，但 unsubscribe 的完成记账还没结束”，或以明确测试注入设置状态。还要单独保留 RUNNING 时 active callback 的 unsubscribe/Stop drain 测试。
- 首包之后才建立新订阅的协调流程，应通过无订阅健康查询来确认首包，不能等待“尚未重新注册的订阅回调”而死锁。§13 现有健康接口足以做到这一点，不需要额外 Preview 占位引用。

以上是测试可达性/证据覆盖审查，不是把已有 callback drain 机制判为失败。

## 6. 有限的规划闭环清单

| 门禁 | 本轮状态 | 可执行的关闭动作 |
| --- | --- | --- |
| icholy 最小适配路线 | 已由用户批准，保持决定 | 不再研究候选；实施时按 design §15 的 normalization/empty opaque/标准 cnonce 测试验证 |
| 两固定模块 provenance | 已闭环：复测取得真实 h1 校验和 | 临时隔离测试完成，真实 Sum/GoModSum 已记录，实施阶段直接 pin |
| Native join/drain/操作存活期 | 设计需收敛 | 父会话将 N1–N5 的所有权、注册/注销、Stop 顺序、token 完成证明写入 §13/§16；无需再泛泛研究线程方案 |
| generation 与 C 输出 | 设计需收敛 | 明确 N6 成功构造才提交、N7 无效尺寸不写/合法尺寸失败值、N8 冲突码及旧 ABI 行为差异 |
| 验证覆盖 | 已给出场景，未执行 | 将各 N 项的屏障/故障注入与现有 ABI/loopback 测试一并纳入 implement.md；只有实现阶段实际运行才可勾选 |
| 启动授权 | 未获得 | 以上规划修订和 provenance 结果经过整体审阅，再取得用户实施批准；本报告不触发 start |

这份闭环不要求先做全量外部安全审计，也不要求本研究阶段跑 Native/Go 生产构建。未来仍需正常单元/集成、race/ASan/UBSan/TSan 和目标部署验证；这些是实施/最终验收，不应混写成此次静态复核已通过。

## 7. 手工检查记录与残余风险

- 已读现有 drain/snapshot 测试源码（native/tests/rtsp_integration_test.cpp:301–386）、纯 C ABI 测试及 Go token 删除测试；它们提供现有行为参照，**本轮没有执行任何测试**。
- 已知剩余风险是：网络阻止模块 h1 验证；设计文字尚未解决 N1–N8；现有 Go handle/token 完成路径存在源码可推导但未运行复现的竞态窗口。没有把源码审查当作修复或独立最终 review。
- 所有项目内写入仅针对 `research/planning-gates.md`。本轮开始对 engine.hpp、go.mod/go.sum、design/implement 及两份先前 RTSP 报告记录保护哈希；结束时复核，避免混入既存工作树修改。

结束检查：7 个受保护文件的 SHA-256 全部与本轮开始一致；指定 engine.hpp 哈希严格匹配。两次下载的实际 Error 与文档一致、没有虚构 Sum/GoModSum；N1–N8 与闭环动作均存在。`git diff --cached --name-only` 为空。task.py current 的无活动任务结果保持不变（脚本 exit 1 表示当前无指针），没有 start。文档/哈希断言通过不代表任何 Native/Go 运行测试通过。
