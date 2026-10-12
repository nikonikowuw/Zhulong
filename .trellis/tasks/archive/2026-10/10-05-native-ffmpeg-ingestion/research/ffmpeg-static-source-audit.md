# FFmpeg 静态接入源码核查

## 来源与版本
- 官方发行目录：https://ffmpeg.org/releases/
- 本任务选用固定维护版本 7.1.5：https://ffmpeg.org/releases/ffmpeg-7.1.5.tar.xz 。不声称这是最新版本，也不依赖最新主版本特性。
- 本次下载 SHA-256：`de668509caf9e35e3cd162473441fdb29538c6d96ed080292b3cf9e6fc5d558f`。
- 摘要为本次通过 HTTPS 获取的归档计算值，可作为后续一致性锁定；未独立验证发布者签名，不能称为签名认证。
- 临时研究源码：`/tmp/zhulong-ffmpeg-research.3pmNFS/ffmpeg-7.1.5`。实施不能依赖此临时路径，须通过正式依赖准备流程获取。

## 已执行的证据
在独立临时目录执行 configure，未编译 FFmpeg、未修改产品代码。当前 GCC 14.2 配置通过，不证明其他编译器或交叉构建已通过。
配置试验：disable-autodetect、disable-everything、disable-programs/doc/avdevice/avfilter/swscale/swresample/postproc，enable-network/static/pic，disable-shared/x86asm，enable-avformat/avcodec/avutil，enable-demuxer=rtsp，enable-protocol=tcp,udp,rtp，enable-parser=h264,hevc，enable-decoder=h264,hevc。
结果：
- 仅 avformat/avcodec/avutil 三个库，无程序/编码器/硬件加速器；许可证输出 LGPL 2.1 or later。
- RTSP 自动选中 HTTP 与 RTP 解包，后者自动选中 asf/mov/mpegts/rm demuxer。不能把请求的 enable 列表当作全部最终能力。
- 仍检测到 libc iconv；源码 configure:4469 明确 disable-autodetect 不阻止 libc iconv 检测。正式配置添加 `--disable-iconv` 并重新核验完整组件清单，避免 Darwin 等平台额外依赖。
- `--disable-x86asm` 用于减少主机 nasm 依赖，不作为 ARM 优化配置；ARM 使用实际目标配置，不启用 host 的 -march=native。
- configure 警告 pkg-config 不存在；正式构建应明确自包含依赖解析，不能忽略警告并宣称完整静态链接已验证。

## 关键源码契约
- `configure:5637-5640` 检测 C11，能力不足时报错；FFmpeg 的 C11 与 Engine 的 C++17 是两个独立要求，不需要人为指定 GCC 14。
- `configure:373-374,5209` 支持 cross-compile 与 sysroot。FFmpeg 与 CMake/CGO 必须使用相同目标工具链及运行库。
- `configure:3681-3682`：RTSP/RTP 自动依赖，见上述组件清单。
- `libavformat/rtsp.c:103`：`timeout` 是 socket I/O 超时，单位微秒；不是无条件覆盖 DNS/全部探测阶段的硬截止时间。
- `libavformat/avformat.h` 提供 interrupt_callback；find_stream_info 可能读取并解码部分数据，所以保留 h264/hevc 软件解码器用于探测，不意味着实现持续软件/VPU 解码流水线。
- `libavformat/httpauth.c:253,273` Basic/Digest 使用 `ff_urldecode(auth, 0)`；父设计 QueryEscape 的加号空格语义不可直接照搬。Digest 对解码后 userinfo 按首个冒号分割，用户名中的冒号不可宣称全面支持；本任务不实现业务层任意裸 URL 消歧。
- timeout 与取消必须覆盖 open/find_stream_info/read/close 测试；同步系统解析行为另查，不能承诺 AVIOInterruptCB 可抢占任意阻塞调用。

## 内存、许可证与交付
- AVFormatContext 由所属 worker 操作并 avformat_close_input；AVPacket 循环 unref，结束 free。不能跨线程并发关闭正在读取的 context。
- LGPL 2.1+ 是源码 LICENSE.md 的默认声明；不启用 GPL/nonfree/version3 选项或外部编解码器。
- 静态分发需保存准确源码、修改、配置、许可通知，并提供满足适用 LGPL 重链接要求的对象/构建材料；只附许可证文本不足以证明完整合规。

## 未验证项
- 全量库编译、最终 Native/Go 链接、真实 RTSP 行为与 sanitizer 尚未执行。
- 本机 command -v 未发现 aarch64-linux-gnu-gcc、arm-linux-gnueabihf-gcc、docker 或 podman。实现前需解决参考交叉工具链的获取；不自动安装系统软件。
- RK3568 镜像、用户态架构与 sysroot 未知，真实板端发布 profile 与运行验收保留 pending。
