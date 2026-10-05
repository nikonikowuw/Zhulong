# 阶段二桥接规划：仓库证据与风险

## 取证范围
2026-10-05，基线 `dev` / `2fbe3ed`，规划前工作区干净。仅阅读、采集版本和创建规划文档；未执行新实现或本任务测试。

已读：父任务三份规划、Native 子任务三份规划及现行 native/backend 规范、公开 engine.h、Go wrapper/tests、Native pipeline/engine.cpp、Python RTSP fixture、Native README。

当前版本命令结果：
- `go version`：go1.27.1 darwin/arm64，go.mod 的 go directive 为 1.27.1。
- `cc --version`：Apple clang 17.0.0 (clang-1700.0.13.5)。
- `python3 --version`：3.9.6。历史 Native README 的 Python >=3.12 和 Darwin 未测试文字早于 2fbe3ed，不能当作当前实测结论；此任务不修改旧任务验证记录。
- FFmpeg 固定 7.1.5；仓库准备器构建静态库，没有依靠系统 ffmpeg CLI，也没有 ZLMediaKit。
- `go doc runtime/cgo.Handle`：令牌是 uintptr，能跨 C 传递并在 Go 取回值；零令牌无效。不可把整数强转成 Go 指针交给 C 保存。

## 当前接口（以代码为准）
- `internal/engine/engine.go`：仅生命周期；一个 mutex 跨 C start/stop/destroy 调用。直接在这个锁内加阻塞 probe 会让 Stop 无法及时进入取消路径。
- `engine_test.go`：明确要求 Close 后可以再次 Start；必须保留。
- `native/include/Zhulong/engine.h`：probe 是 `Zhulong_engine_probe(engine, url, options, out_result)`，不是父草案的独立 `Zhulong_stream_probe`；result-view-destroy 三件套，extradata 动态。
- acquire 传整数 consumer_id、kind，返回 stream_id。subscribe 传 Preview consumer_id、函数指针与 uintptr_t token，返回 subscription_id。每个消费者最多一个订阅。
- `Zhulong_packet_view` 有 codec、int64 pts/dts、has_pts/has_dts、time_base、key_frame、data/size。没有保证统一的 NALU 封装格式。
- state 只有 STARTING/RUNNING/FAILED，通过 get_status 查询，没有 Native 状态回调。
- ABI 的 IO 错误尚不区分 RTSP 401/403；桥接不可生成虚假的 AUTH_FAILED。

## 生命周期证据
- Native stop：先 running=false，取消流与已注册探测，join reaper/worker，等待 probes 清空；destroy 必须和所有外部操作串行。
- Native probe：在 control + probe_mutex 下注册取消对象，退出时 RAII 移除；Stop 在 probe 入场前执行会导致 probe 得到 NOT_RUNNING。
- Native unsubscribe：先移出订阅表，解开池锁，再 disable_and_drain；仅成功 drain 或已确认全引擎 Stop 完成后，才能回收 Go handle。
- 每个 worker 同步执行 Go callback；回调内控制接口返回 CALLBACK_CONTEXT，但如果 Go 先拿一个被 Stop 持有的锁，仍可能在抵达 C 检测前死锁。
- 本任务避免暴露任意用户同步回调，内部 callback 只检查、复制和入队；不能在 callback 内同步清理自己。
- Native 失败流在引用存在或 8s grace 内保持 failed，再 acquire 不重启。后续业务不能简单用 1s 重获取实现自愈，亦不能为单摄像机故障 Stop 全局 Engine。

## 取消方案取舍（拟采用，无新 ABI）
共享 Engine 只有全局 stop，不能用它取消单次 Go request。拟每个 probe 创建独立、临时 Native engine，启动后注册到 Go 父 Engine 的操作集合；上下文取消只 stop 该私有 engine。最多 4 个在执行的 probe，等待名额可取消。

每次多一个 Native 生命周期/reaper 是明确代价；无媒体订阅，不加入共享流池。必须先 start 再允许 stop，取消不会在 stop 后重新 start；stop 与 probe 竞争由已实现 NOT_RUNNING/CANCELLED 语义收敛。父引擎关闭取消并等待所有私有 probe，禁止返回后残留 goroutine。DNS/任意系统调用不可抢占的限制仍然存在。

备选：扩展 Native 单 probe cancel ABI，可减少私有 engine 开销，但会扩大已交付 ABI 和旧任务范围；暂不采用。仅“Go goroutine 提前返回，让 C 自己超时”不是安全替代方案。

## 测试复用
`native/tests/rtsp_fixture.py` 的 Server / Handler 可导入，导入不会运行 main。它已提供 loopback 端口、H.264/H.265 RTP、TCP/UDP、stall-open/info/read、large-extra 和连接计数。

其 CLI main 固定启动一个 C++ 测试程序并断言 `/pool` 的次数，不能直接用 Go test 代替该可执行程序。计划增加测试用 runner 复用 Server，而非复制整个协议服务或更改已有 CTest 断言。runner 以环境变量给 Go 用例传入合成测试地址，回收子进程/fixture 并检查双通道输出、连接计数和零活跃连接。

## 父计划不能直接照抄的内容（后续子任务门禁）
1. 旧 C ABI 类型/错误码、512-byte extradata、字符串 consumer_id 已被实际头文件取代。
2. URL 用 QueryEscape 会把空格变 `+`，与 FFmpeg userinfo 解码不等价；全串 LastIndex('@') 会误认路径/查询中的 @。歧义输入应引导结构化字段，不承诺任意裸 URL 可无歧义逆向解析。
3. WebSocket 认证应接入仓库现有内存 Session + HttpOnly Cookie，不采用草案 JWT；还需 Origin/会话过期/注销处理。
4. 压缩包异步持有需复制；wire 协议需版本、codec 配置、PTS/DTS/存在位/时间基和长度上界，不能只有 uint64 单调 PTS + 原始字节。
5. “有预览才订阅”不等于“摄像机启用即永久拉流”；状态观测、空闲关闭与重连需求需业务任务审阅。
6. AES-GCM 独立密钥的生成、权限、恢复及迁移计划尚未设计，不可复用管理员密码哈希或隐式硬编码密钥。

## 工作流路由说明
仓库与当前用户技能目录未找到可加载的 `trellis-brainstorm/SKILL.md`；使用 `.trellis/workflow.md` / get_context 1.1 的明确规划规则（先研究、逐问、持久化、审阅后 start），不声称已加载不存在的技能。已加载 golang-pro、ffmpeg-streaming 及相关 reference；未委派子代理、未进入执行阶段。
`get_context.py --mode packages` 显示 package spec 未配置，但实际 `.trellis/spec/backend/` 与 `.trellis/spec/native/` 存在，清单直接引用这些规范，不改项目配置。
