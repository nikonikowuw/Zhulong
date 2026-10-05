# Go/CGO 桥接实施计划（已批准）

## 规划门禁
- [x] 用户选择 A：保留 Native 待验收项，推进媒体阶段二。
- [x] 已创建父任务下的独立桥接子任务；父任务记录摄像机/分发后续交付，不提前创建业务代码目录。
- [x] 已核对真实 ABI、Go/工具链版本、生命周期测试、fixture 和过时父草案的冲突。
- [x] 已完成 PRD、design、implement 草案。
- [x] implement/check JSONL 各 11 条规范/研究上下文；task.py validate 通过，git diff --check 通过。
- [x] 用户于 2026-10-05 在规划摘要后回复“开始实现”，批准本子任务设计与实施计划。
- [x] task.py validate 通过，task.py start 已将状态切为 in_progress；尚未开始产品改动。
- [ ] 收尾前处理分支元数据：start 提示当前 branch/base_branch 同为 dev，PR/归档会被拒绝。当前不擅自切分支或改目标，提交阶段交由用户确认。

## 执行前置
- 在 `dev` 上重新确认 git status / diff / HEAD，不改动其他会话的文件；本计划基线是 2fbe3ed，不依赖其他主机上的临时 build 证据。
- 依次读取 curated JSONL → prd → design → implement；按需加载 golang-pro、ffmpeg-streaming 和 simplify。
- 旧 Native 任务仍 in_progress。不得归档、删除交叉/板端验收，也不得以父任务归档作为本任务开始条件。
- 本任务目标是宿主 Go 桥接；跨工具链或板端问题不得扩成自动下载 SDK/安装系统工具。

## 执行快照与写入边界（2026-10-05）
- 基线：`dev` / `2fbe3edcbd03608945b58b3bc783f4d60960adb4`。
- 执行前快照：`/var/folders/17/jbddfmk554l6gv2fl0t_90qh0000gn/T/zhulong-go-bridge-before.7eviv97q`；含 tracked diff、状态、任务规划副本及受保护头文件。
- 既存 `native/src/pipeline/engine.hpp` 修改去除空白后与 HEAD 相同，属外部格式修改；保留原样，不纳入本任务改写。快照 SHA-256：`dad479e08adc077e9c5c484c8ebcd87964c4eb6683d669cb838944dc21256148`。
- 拓扑判定：single-seam。Go Engine 准入、Probe、Stream、Subscription 共同依赖同一代次/销毁/drain 契约，拆写者会重叠修改同一资源登记与锁边界；RTSP runner/Make 门禁是该契约的测试配套，不是新的产品交付。

| 执行阶段 | repo/cwd | 独占范围 | 隔离/权限 | 下一门禁与交接 |
| --- | --- | --- | --- | --- |
| bridge-implement | `/Users/niko/dev/go/Zhulong` | internal/engine、桥接测试 runner、必要 fixture 测试扩展、Make go-check 最小接线、任务实施记录 | 共享 cwd 串行唯一写者；不改 Native 产品 ABI/实现，不动既存修改、不 stage/commit | 聚焦 race/cgocheck2/真实 RTSP；输出 runtime-managed 实施报告 |
| bridge-check | 同上 | 同一交付全范围检查；仅修复本任务范围问题 | 实现结束后新上下文，串行接管写权限 | 完整 make check/smoke、遗留问题与规范更新建议；输出 runtime-managed 检查报告 |
| parent-finish | 同上 | 证据汇总、规范更新、提交审批 | 两个子阶段都结束后才写 | Phase 3.3 / 3.4；未授权 commit/push |

## 1. Go 数据契约与错误（AC1/2/5/9）
- [x] 增加 Go 原生 options、consumer/codec/state 枚举、Rational、VideoInfo/Packet、类型化错误；只对真实 Native 状态做映射。
- [x] 编写表驱动校验：NUL、非法枚举/ID、duration 负值/溢出、deadline 剩余不足、size_t/长度上界、nil/0 长度、未知 FPS/PTS/DTS、负时间戳。
- [x] 不引入 Gin/GORM/Fx 到 engine，不改 go.mod 工具链版本。

## 2. Engine 操作准入与资源代次（AC1/4/8）
- [x] 分离短时状态与长时 C 调用锁边界；登记在途操作/私有 probe；关闭准入先于取消与等待。
- [x] Start/Stop/Close 串行转换；Stop/Close 时 Ready=false；旧 wrapper 由 generation 拒绝，Close 后可再 Start。
- [x] 并发生命周期/操作测试；禁止持 callback 或清理方需要的锁执行 stop/unsubscribe/join。

## 3. 独立 probe（AC2/5/7）
- [x] 实现私有 Native engine 探测、最多 4 个并发准入、ctx/deadline 收敛、结果复制与成对销毁。
- [x] 验证取消前/注册竞争/阻塞 open/info、stop 和 probe 完成同时到达；主流与另一 probe 不被误停。
- [x] 返回前等待 worker 及 C 操作退出；测试和文档明确 DNS 非硬抢占限制。

## 4. 流消费封装（AC3/4）
- [x] Acquire 返回消费者 wrapper，Status 映射真实状态，Close 保持 unsubscribe → release；不隐式重连、不改 8s grace。
- [x] 测试同 URL 多消费者、重复 ID、transport/timeout 冲突、非 Preview 订阅拒绝、重复 Close、restart 旧对象。

## 5. C→Go 订阅、缓存与清理（AC4/5/6/8）
- [x] 独立 C shim + 声明式 export preamble，整数 cgo.Handle；发布/回滚覆盖首次 callback 早于 subscribe 返回的竞争。
- [x] callback 仅借用、校验、受限复制和入队，不执行用户回调/IO/同步控制 API。
- [x] 内部队列同时限制包数/字节/单包长度；Next 取出时扣预算；关闭唤醒读者并清空积压。
- [x] 每订阅最多一个控制协程，处理 ctx/Close/过载及状态轮询；无逐包 goroutine。
- [x] successful drain / engine stop 确认后恰好一次 Delete。未知清理失败不得推测 drain 完成。
- [x] mock/可控测试缝覆盖异常 size、panic 保护、token 分配/释放账本；真实路径验证回调后持有的数据不变。

## 6. 真实 RTSP 集成门禁（AC2/3/4/6/7/8/9）
- [x] 新增 `native/tests/run_go_bridge_tests.py`（测试工具，非生产入口），复用现有 fixture.Server，不复制 RTSP 协议实现、不替换已有 CTest。
- [x] runner 为 Go 集成测试提供动态 loopback 地址，运行 wrapper 注入静态链接，限制总时长并回收测试进程与 fixture；失败传播非零。
- [x] 连接计数验证池复用/grace/最终零活跃连接；采集 stdout/stderr 确认无合成凭据泄漏。
- [x] 覆盖 H.264/H.265、TCP/UDP、large-extra、open/info/read 阻塞、慢/快两个订阅、probe 取消与持续预览隔离、Stop/Close 后零在途 callback。
- [x] 将 runner 纳入 `make go-check`，因此 `make check` 不会因缺环境变量静默跳过真实桥接测试。直接 Go 单元测试的 integration skip 必须明确，不能作为全任务通过证据。
- [x] Go race 与 cgocheck2 分别记录。fixture 故障/依赖缺失不是通过，交叉产物不得被测试 runner 在宿主执行。

## 验证命令
以下前两项为已有入口，可在实施后使用；其余新入口需第 6 步实现，不是本轮已经运行的测试。

```bash
# Bash；依赖仓库 Go 1.27.1 / Python / CMake / C/C++ / 已准备静态 FFmpeg。
python3 native/scripts/build.py go test -race -count=1 -timeout=120s ./internal/engine
make native-test

# 待实现：runner 内部经 build.py 运行真实桥接集成测试，固定 test timeout。
python3 native/tests/run_go_bridge_tests.py
GOEXPERIMENT=cgocheck2 python3 native/tests/run_go_bridge_tests.py

make go-check
make check
make smoke
```

CGO 指针增强检查用 GOEXPERIMENT=cgocheck2，不使用已淘汰的 GODEBUG=cgocheck=2。构建脚本是否保留 GOEXPERIMENT 在实施时核对；若拦截，不绕开静态链接入口，修正合法传递并补测试。
Native 产品代码原则上不改；若必须修改，先回规划确认，按 native/README 的独立地址/未定义/线程 sanitizer 路径执行，不能用 Go race 代替。

## 7. 质量检查、规范与提交门禁
- [x] 简化新代码但不改变行为；进行全范围 check（backend + native + 集成构建入口），不得只查最后一块修改。
- [x] 记录确切命令、退出码、环境、连接/token/worker 清理证据与未验证事项；本任务测试过后才勾选 PRD。
- [x] 更新已改变的 cgo-contract / ingestion-contract 中“Go 仅生命周期”的现状，新增 Go 使用/所有权契约；不顺带把过时历史记录改成未经验证的成功。
- [ ] 按 Phase 3.4 请求提交确认，不自行 commit/push；通过后再按 finish-work 收尾。
- [ ] 向父任务回填 Step 2.1 验收，并把实际 API、取消/失败/缓存边界移交摄像机子任务。旧 Native 任务不因此完成。

## 委派规则（实施获批后）
主会话使用 pi-subagents 的 trellis-implement / trellis-check；提示首行 `Active task: .trellis/tasks/10-05-go-cgo-media-bridge`。先核验可用 agent；同 cwd 一次只允许一个写者，子代理不得递归启动 implement/check。若采用多步编排，使用唯一顶层 async workflow 并在依赖边界消费结果。遇执行基础设施失败先保存证据并停止，不切换其他 CLI 执行协议。

## 回退点
- 数据/生命周期变更失败：保留基线测试与既有 Native 代码，撤回本任务 Go 增量（须确认无其他会话改动）。
- drain/token/取消存在不确定性：停止扩大实现，回设计修订；不能用 recover 吞没失效 token 或提前 Delete 换取测试通过。
- 需要 ABI 变更/浏览器协议/数据库凭据决策：交回对应范围评审，不在桥接子任务偷偷扩展。
