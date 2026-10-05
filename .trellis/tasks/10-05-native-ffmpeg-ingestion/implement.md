# 实施计划（已批准）

用户已批准最终计划；执行前运行 task.py start。

## 规划门禁
- [x] 确认 C++17 功能验证，不预设 GCC 最低版本。
- [x] 确认开发主机交叉编译，Engine 增量构建，板端不编译。
- [x] 记录 RK3568、疑似 Debian、暂缺板卡；目标用户态/镜像待验证。
- [x] 显式联网准备已确认；保留现有 Darwin 路径，不扩大未验证兼容承诺。
- [x] 固定 FFmpeg 7.1.5，已完成源码/configure 研究并持久化；完整构建仍待实施。
- [x] ABI 决策记录在 design.md 第 6 节；新增函数签名随实现接受纯 C 测试。
- [x] 上下文清单校验通过，PRD 与最终规划摘要已获用户批准。

## 实施顺序（批准后）
1. 快照 dirty paths/diff，确认其他会话修改；不覆盖已有 Makefile/Go 业务修改。
2. 实现显式依赖准备、静态 FFmpeg 配置和缓存；验证缺依赖时明确失败，无共享库回退。
3. 加入 host/cross 隔离与 CMake/FFmpeg/CGO 一致目标传递；验证主机库不会被交叉链接读入。
4. 扩展纯 C ABI 与生命周期测试；先覆盖无效参数、错误码、释放与异常边界。
5. 实现独立探测与采集：RAII、中断、凭据安全、参数集及时间基。
6. 实现物理流池、消费者引用、8 秒释放与取消、订阅 drain；加入并发压力和停止路径测试。
7. 使用可重复本地 RTSP 测试源验证 H.264/H.265 与阻塞/超时；mock 只验证池逻辑，不能替代真实 libavformat 路径。
8. 验证最终 Go 链接、Engine 修改后的增量构建以及 FFmpeg 缓存复用。
9. 全范围检查、规范更新判断，提交计划需另行确认；保留板端未验证项。

## 验证命令
现有宿主入口（实际静态依赖接入后执行）：
```bash
make native-build
make native-test
make go-check
make check
```
新增入口已实现：`make native-cross-build CROSS_PROFILE=... CROSS_OUTPUT=build/targets/NAME/Zhulong`；配置格式见 `native/profiles/linux-arm64.example.json` 与 `native/README.md`。示例不是 RK3568 发布 profile，缺工具链/sysroot 时失败，不回退宿主依赖。
对目标产物使用工具链 readelf 验证 ELF machine、interpreter、NEEDED 与 version-info；对依赖归档抽样检查目标对象架构。
Sanitizer 分别运行 ASan/UBSan、TSan（依工具链支持），结合 CTest 进程超时防止测试永久挂起；Go race 不替代 C++ 数据竞争检查。

## 风险与回退点
- FFmpeg 版本/工具链不兼容：停在依赖准备阶段，不静默换新 GCC 或动态库。
- CGO 固定库路径需调整：最小改动，宿主回归先行；切勿混用旧缓存导致 Engine 改动未进入最终二进制。
- 跨编译成功但无板端证据：记录 build-only，不宣称 RK3568 部署通过。
- 发现需要 VPU/NPU 或其他业务功能：回到父任务范围评审，不扩大本子任务。

## 实施记录（2026-10-05，任务保持 in_progress）

- [x] 读取全部任务/curated JSONL、before-dev、ffmpeg-streaming 及相关规范；使用父会话快照 `/tmp/zhulong-native-before.KBPEmA`，后续恢复检查快照 `/tmp/zhulong-native-stall.LVeR3O`。未 commit/stash/reset/切分支/安装系统包；未覆盖无关修改。
- [x] 显式下载并核对固定 7.1.5 SHA-256；离线构建 avformat/avcodec/avutil。最终依赖路径 `build/deps/ffmpeg/host/ad5efe893b6165459573d242/install/`，完整配置、源码、许可文本及构建日志保留。
- [x] 实现 host/cross 隔离、显式工具链/sysroot/Go ABI 传递、内容身份缓存、目标 `.pc` 系统库传播（含需要时的 libatomic），缺依赖与混用宿主库拒绝回归。实际 target 编译另列未完成。
- [x] 纯 C ABI/异常隔离/输出清空；独立 probe、元数据动态 extradata、TCP/UDP、源时基与缺失 PTS/DTS 标记；原 Go 生命周期 API 不变，没有新增 Go 订阅 API。
- [x] 实现单连接消费者去重、配置冲突、8 秒 grace/reacquire 取消、stop/probe cancellation、reaper/worker join 和 callback drain。自检后收敛为独立生命周期 gate；pool 锁不跨 join/drain，测试断言 drain 期间 status 调用不被 pool 锁阻塞。
- [x] 真正经过固定静态 libavformat 的本地 RTSP/RTP fixture 验证 H.264/H.265 probe/packet、UDP、>512-byte extradata、open/info/read 截止与阻塞取消、TEARDOWN 无响应、连接计数、并发消费者、无订阅无回调、回调重入拒绝。不是 mock 替代网络路径。
- [x] 修复实测发现的 `find_stream_info` 中断后可返回部分成功：成功返回也检查绝对 deadline/cancel。初始 RTP 包可无时间戳，测试按显式 absence flag 而非伪造 PTS 验收。
- [x] `make native-test`：5/5 CTests；build contract 包含 9 个 Python 子测试。ASan/UBSan 与 TSan 分离目录各 5/5；只声明 Engine/test instrumentation，未把普通缓存 FFmpeg 当成已 instrumented。
- [x] `make check` 最终通过：frontend lint/types、36 tests、audit 0 vulnerabilities/build；Native 5/5；Swagger；Go vet/race。`make smoke` 最终真实 Go 链接及健康/SPA/API/Swagger/停机通过。
- [x] `python3 native/tests/verify_incremental.py --allow-source-edit`：临时 ABI marker 进入修改后的真实 Go 可执行文件，恢复后消失；仅 ABI 对象重编、FFmpeg 所有归档 hash/mtime 不变、CGO link flags 随归档摘要变化。已恢复源文件。证据 `build/validation/incremental-evidence.json`。
- [x] Host ELF/NEEDED/version-info 与三归档对象抽样检查：x86_64、无 FFmpeg 共享库依赖；主机产物需要最高 GLIBC_2.38 / GLIBCXX_3.4.30，不适用于未知旧镜像兼容承诺。见 `build/validation/final-elf.txt`、`dependency-audit.json`。
- [x] 更新 native/backend 受影响规范、README、构建/ABI 可执行合同；使用 simplify 判断整理 shutdown 锁边界与缓存/目标参数，不扩展业务功能。
- [ ] **实际目标交叉编译及 target ELF 验收仍阻塞**：缺 aarch64-linux-gnu-gcc、arm-linux-gnueabihf-gcc、容器工具、目标 sysroot。`make native-cross-build CROSS_PROFILE=native/profiles/linux-arm64.example.json CROSS_OUTPUT=build/targets/linux-arm64-example/Zhulong` 明确失败（make exit 2：需要独立存在的 sysroot），见 `build/validation/cross-blocked.log`。未下载未经批准工具链，也未删去该验收项。
- [ ] RK3568 Debian 用户态/镜像、发布 profile、实际板端运行与部署许可重链接材料验收仍 pending；Darwin 新路径未实测。不得把任务标记完整完成或 archive。

最终宿主证据：`build/validation/{full-check-final,asan-ubsan-final,tsan-final,smoke-final,incremental-final}.log`；`full-check-final.log` 包含最后一次 `make native-test`。最终 FFmpeg 上游编译日志有 19 条警告（包含 GCC 优化器边界诊断），未修改第三方源码；Engine 本身无编译警告。原始源码仅 HTTPS+摘要一致性校验，未验证发布者签名。

## 独立检查记录（2026-10-05，仍不满足完整跨平台验收）

- 恢复后的 trellis-check 已独立阅读全部 curated context、approved artifacts、实际代码及实施日志；没有继续使用失效的 1ms deadline，也没有启动子代理或切换执行模式。
- 修复四项 P2：环境编译器搜索路径/CGO flags 可绕过 sysroot 与缓存身份；CMake 未接收 profile 的 ar/ranlib；重复 Go `-o` 或后置 `-c=false` 可绕过交叉输出/禁止执行规则；默认 UBSan recover 模式可打印违规后 exit 0。前两类构建回归保留失败前日志，UBSan 用最小违规程序证明修复后非零退出。
- 构建合同现有 12 个 Python tests；补充真实 worker snapshot 中注销后续 callback、抛异常 callback 后 drain、engine destroy 后 probe-result 缓冲仍有效，以及 stdout/stderr 双通道凭据断言。未改动产品 C++ 生命周期/采集实现，没有新增 Go 订阅 API。
- 最终 `make native-test`、ASan/UBSan、TSan 各 5/5 CTests；`make check`（含 frontend 36 tests、audit 0、Native、Swagger、Go vet/race）和 `make smoke` exit 0。`verify_incremental.py --allow-source-edit` 再次证实仅 ABI 对象重编、FFmpeg hash/mtime 不变、真实 Go binary marker 进入/恢复；源文件已恢复。
- build.py 身份变更触发正式离线 FFmpeg 重建，检查后的最终 prefix 为 `build/deps/ffmpeg/host/44a5403bf2dc4518775f45f9/install/`，取代上述实施阶段的旧 final prefix；旧日志/缓存保留。独立检查了全部 291 个 FFmpeg 归档对象架构、最终 Go ELF/符号与许可配置：x86_64，无 FFmpeg shared NEEDED，GLIBC_2.38 / GLIBCXX_3.4.30，上游仍有 19 条 warning。
- 检查证据位于 `build/validation/review/`：`native-final-v2.log`、`asan-ubsan-final-v2.log`、`tsan-final-v2.log`、`full-check-final.log`、`smoke-final.log`、`incremental-final.log`、`artifact-audit.json`、`ubsan-gate.log`。交叉入口仍 exit 2（missing sysroot）；**未完成真实 target link/ELF 或 RK3568 runtime 验收**。FFmpeg 自身未被 sanitizer instrumented，板端/Darwin/完整 LGPL 重链接分发材料继续 pending。
