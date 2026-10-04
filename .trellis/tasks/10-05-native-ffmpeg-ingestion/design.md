# Native 与静态 FFmpeg 接入设计（已批准）

状态：用户已在最终摘要后明确回复“开始实现”，批准进入实施。源码研究见 research/ffmpeg-static-source-audit.md；板端 profile 和实测仍待环境证据。

## 1. 构建边界
开发主机负责 FFmpeg → Engine → Go/CGO 的全部编译和最终链接；板端只部署与测试。
- host 构建：以当前主机工具链运行 Native/Go 测试；不证明目标板兼容。
- cross 构建：显式提供目标工具链、sysroot、Go 目标架构；配置失败时不回退宿主库。
- RK3568 的 Debian 用户态位数未知，不预填已认证的 arm64 发布 profile。
- 保留现有 C++17，检查真实语言/库功能而非强制 GCC 最低版本。
- 宿主与目标使用不同构建目录、依赖安装前缀与 CGO 链接路径。当前 Go 固定引用 build/native，需最小修改以支持目标选择，禁止通过覆盖宿主归档切换目标。
- CMake 使用 toolchain 文件传递编译器/sysroot 与库搜索约束；FFmpeg 使用自身 configure 的交叉选项。两者必须共享同一目标契约。
- pkg-config 如参与解析，只搜索目标依赖目录，不能读入宿主 .pc 文件。最终 Go 外部链接必须使用目标工具链。

## 2. 依赖与增量构建
FFmpeg 从固定官方源码版本构建 avformat/avcodec/avutil 静态库；固定版本为 7.1.5，源码摘要和已核查裁剪配置见 research/ffmpeg-static-source-audit.md；实际完整编译仍需验证。禁用隐式共享库回退。
- 缓存身份至少含：源码摘要、补丁、裁剪参数、目标架构/ABI、编译器身份及 sysroot 身份。
- 缓存身份不能只用工具链路径，路径下 SDK 更新也必须使缓存失效。
- Engine 源码修改只触发依赖对象重编与最终 Go 重链接，不重建未变更 FFmpeg。
- 不使用 -march=native 生成目标发布库。
- CMake 的 STATIC target 链接依赖不代表归档已经物理合并；将完整 FFmpeg 及必要系统库依赖传递到最终 Go 链接。
- FFmpeg 静态内嵌不等于整个可执行文件全静态；glibc/libstdc++ 与解释器兼容单列检查。许可证、对应源码及适用的重链接材料需纳入交付设计。
- 显式联网准备 + 本地缓存方案已获用户确认；常规配置/构建不得隐式下载。

## 3. Native 生命周期与内存
- 保留现有生命周期 ABI 与错误码；公开头文件只包含纯 C 类型，导出函数隔离全部 C++ 异常。
- FFmpeg format context 由采集 worker 单线程操作和关闭；停止方只设置取消标志并唤醒等待，不能并发关闭 worker 正在使用的 context。
- 连接/探测使用单调时钟绝对截止时间；持续采集另设无数据截止时间。为 open/find-stream-info/read/close 的阻塞路径逐项核查中断能力，不笼统承诺所有 DNS 和系统调用可由 AVIOInterruptCB 抢占。
- AVPacket 由 Native 分配、unref/free；回调数据仅在回调期间有效。C++ 不保存 Go 指针，仅保存整数令牌。
- 退订阻止新回调并等待已进入的回调退出，完成后未来 Go 桥接才可删除 cgo.Handle。不得持 pool 锁执行用户回调或 join。
- 回调内同步退订/stop 的自等待风险必须有明确 ABI 限制或拒绝语义；不能隐藏死锁。
- 同步回调契约要求消费者有界、非阻塞，不在回调直接进行 WebSocket 网络写入。跨调用保留数据的消费者自行复制。

## 4. 流池与元数据
- 以规范化 URL 去重；凭据/路径/查询的大小写和转义语义不得破坏。原始模糊 URL 消歧属于后续业务层；本 ABI 输入契约与测试需明确。
- consumer_id 去重，引用归零进入 8 秒 grace period；新引用取消释放。采集与定时器均有显式停止/汇合路径。
- TCP 默认、UDP 显式配置；同 URL 不同传输参数返回配置冲突错误，不静默混用。
- 只接受目标视频流 H.264/H.265，记录 stream time_base；未知 FPS/缺失参数集不得伪造。
- 不采用固定 512 字节 extradata：probe 输出为 Native-owned 不透明结果句柄，纯 C 只读 view 返回元数据及 extradata 指针/长度，配对 probe-result-destroy 释放；view 在结果销毁后失效。探测失败清空输出句柄，禁止静默截断。
- 不实现 DecodeNode，不采用父设计解码前丢 B/P 包策略。录像、VPU、NPU、重连业务状态机和 Web 广播不在本子任务。

## 5. 验证分层与回退
宿主 CTest/Go 回归 → 指定工具链交叉编译 → ELF/依赖检查 → 实际板端 smoke，四层分别记录证据。
- 交叉产物不在宿主直接执行；QEMU（若使用）只能补充用户态测试，不替代板端验证。
- 板卡不在手边时继续通用设计；目标 profile/板端结果标记 pending，不能归档成完整板端交付。
- 不替换系统 GCC，不修改板卡环境，不覆盖现有未提交业务修改。

## 6. ABI 收敛决策
- 新增函数继续使用 Zhulong_ 前缀及纯 C 类型；既有 -1/-2/-99 错误码不改变。为 unsupported、not-found、duplicate、config-conflict、timeout、callback-context 新增不冲突的具名错误码。
- packet view 包含 codec、PTS/DTS、time_base 分子/分母、关键帧标记、payload 指针/长度；缺失时间戳显式标志，不把 AV_NOPTS_VALUE 当合法时间。
- acquire 对重复 consumer_id 返回 duplicate，不增加引用；release 不存在的 consumer 返回 not-found。订阅不隐式增加消费者计数，要求存在有效 Preview 引用；后续 Go 包装必须按 acquire/subscribe 与 unsubscribe/release 顺序使用。
- 同步注销 drain 所有在途回调。回调内禁止调用控制/销毁 API；可返回状态的控制接口检测后返回 callback-context 错误。destroy 由外部所有者串行执行，不能与其他控制调用并发，更不能从回调调用；头文件明确约束。
- worker 错误上报使用受控错误码和安全固定文本，不能把 FFmpeg 原始 URL/错误日志透传。默认不输出 FFmpeg 原始日志，诊断方案不得泄露凭据。
- FPS 使用有理数且允许 unknown；宽高或 codec 无法取得则 probe 失败。参数集按 FFmpeg extradata 保留原始表示，不冒称总是 Annex B，也不自动把所有包转换格式。
- NormalizeRtspUrl 只处理合法 URI 的协议/主机/默认端口与外围空白，保留路径/查询/凭据语义；不会用全串最后一个 @ 解析含歧义的裸凭据。
- 不增加自动重连业务逻辑；采集失败上报状态，恢复由后续业务层管理。
