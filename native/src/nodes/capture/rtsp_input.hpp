/**
 * @file rtsp_input.hpp
 * @brief Zhulong RTSP 采集节点与 FFmpeg libav* 交互接口定义
 *
 * 核心机制说明：
 * 1. 协作式中断与超时（Cancellation & Deadlines）：
 *    - 封装了基于单调时钟 steady_clock 的绝对截止时间管理与 atomic<bool> 取消信号；
 *    - 通过 FFmpeg 的 interrupt_callback 在底层网络阻塞（如 TCP 握手、RTSP DESCRIBE、av_read_frame）中实现毫秒级快速打断。
 * 2. 视频元数据与内存生命周期（Video & extradata）：
 *    - 内部用 std::vector<uint8_t> 深拷贝保留 SPS/PPS/VPS 等 extradata；
 *    - borrowed_view() 输出供 C ABI 借用的指针视图，避免不必要的数据复制。
 * 3. 安全 URL 规范化（normalize_rtsp_url）：
 *    - 严格校验并规范化 RTSP URL（过滤空白、防止转义绕过、规范 IPv4/IPv6/端口格式、防止凭证打印泄漏）。
 */

#pragma once

#include "Zhulong/engine.h"

#include <atomic>
#include <chrono>
#include <functional>
#include <string>
#include <vector>

namespace zhulong {

/** @brief 使用单调递增时钟，不受系统墙上时钟调整（NTP跳变）影响 */
using Clock = std::chrono::steady_clock;

/** @brief 默认网络建连与空闲读取超时时间（5000ms） */
constexpr uint32_t default_timeout_ms = 5000;

/** @brief 最后一个消费者释放后的流宽限期（8 秒） */
constexpr auto stream_grace_period = std::chrono::seconds(8);

/**
 * @brief 内部业务异常包装结构体
 */
struct Failure {
    Zhulong_status_t status; /**< 对应的 C ABI 错误码 */
};

/**
 * @brief C++ 内部流选项配置
 */
struct Options {
    int32_t transport = Zhulong_TRANSPORT_TCP; /**< 传输协议（TCP / UDP） */
    uint32_t open_timeout_ms = default_timeout_ms; /**< 建连与解析流元数据超时 */
    uint32_t idle_timeout_ms = default_timeout_ms; /**< 帧读取空闲超时 */

    bool operator==(const Options &other) const;
};

/**
 * @brief 解析并校验 C ABI 传入的流选项结构体
 * @param options C ABI 选项指针（为 nullptr 时返回默认配置）
 * @return Options 校验并规范化后的内部配置
 * @throws Failure 若参数不合法抛出 Zhulong_ERR_INVALID_ARGUMENT
 */
Options parse_options(const Zhulong_stream_options *options);

/**
 * @brief 严格校验并规范化 RTSP URL 字符串
 *
 * 规范化规则：
 *   - 剥离首尾空白字符；
 *   - 校验 scheme 为 rtsp://（大小写不敏感）；
 *   - 校验 host 格式（支持 IPv4 域名及带括号的 IPv6 字面量）；
 *   - 规范化默认端口（554 自动省略）；
 *   - 校验并保留合法的百分号转义，拒绝不可见控制字符和非法符号（如 '#'、'\'）。
 *
 * @param url 待规范化的原始 URL 字符串
 * @return std::string 规范化后的安全 URL
 * @throws Failure 若校验失败抛出 Zhulong_ERR_INVALID_ARGUMENT
 */
std::string normalize_rtsp_url(const char *url);

/**
 * @brief 跨线程协作式取消与超时控制状态
 *
 * 线程访问规则：
 *   - stopped 标志支持跨线程原子写入；
 *   - deadline 仅由拥有该 Input 的单个 Worker 线程读写；
 *   - interrupted() 由 FFmpeg 中断回调在网络 IO 期间周期性轮询。
 */
struct Cancellation {
    std::atomic<bool> stopped{false}; /**< 原子停止标志 */
    Clock::time_point deadline;        /**< 绝对截止时间点 */

    /** @brief 从当前时刻起设置指定毫秒后的超时截止点 */
    void expire_after(uint32_t milliseconds);

    /** @brief 检查是否已被外部中断或已超过设定的截止时间 */
    bool interrupted() const noexcept;
};

/**
 * @brief 探测到的视频流元数据容器
 */
struct Video {
    Zhulong_video_view view{};        /**< C ABI 兼容的视频视图数据 */
    std::vector<uint8_t> extradata;   /**< 内部持久化的参数集（SPS/PPS/VPS）二进制数据 */

    /**
     * @brief 生成供外部借用的视图，其中的 extradata 指针指向内部 vector
     */
    Zhulong_video_view borrowed_view() const;
};

/**
 * @brief 同步读取 RTSP 流的核心驱动函数
 *
 * 工作模式：
 * 1. 探测模式（consume 为空）：
 *    - 仅打开连接、解析流信息并提取视频元数据，随后立即优雅断开并返回 Video。
 * 2. 持续拉流模式（consume 提供了回调函数）：
 *    - 打开连接后触发 ready 回调；
 *    - 进入帧循环读取 av_read_frame，并将视频包通过 consume 回调派发，直到 cancel 被触发或发生错误。
 *
 * @param url 规范化后的 RTSP URL
 * @param options 拉流选项
 * @param cancel 协作式取消与超时控制器（生命周期必须覆盖整个读取过程与清理阶段）
 * @param consume 数据包派发回调（可选）
 * @param ready 连接建立就绪回调（可选）
 * @return Video 视频元数据
 * @throws Failure 底层 IO 失败、格式不支持、超时或已取消时抛出对应的 Failure 异常
 */
Video read_rtsp(const std::string &url, const Options &options, Cancellation &cancel,
                const std::function<void(const Zhulong_packet_view &)> &consume = {},
                const std::function<void()> &ready = {});

/**
 * @brief 全局初始化 FFmpeg 库环境（网络模块与静默日志配置，具备进程全局单次执行保证）
 */
void initialize_ffmpeg();

} // namespace zhulong
