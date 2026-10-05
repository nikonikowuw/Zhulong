/**
 * @file rtsp_input.cpp
 * @brief Zhulong RTSP 采集节点底层 FFmpeg 接入与拉流实现
 *
 * 核心实现与安全设计：
 * 1. FFmpeg 中断与网络超时控制：
 *    - 针对底层阻塞式网络套接字操作，绑定 interrupt_callback。当超时到达或被外部取消时，返回 1 立即中断 FFmpeg；
 *    - 析构守卫（Input RAII）：关闭连接前先置 cancel.stopped=true，防止在 RTSP TEARDOWN 时对端不响应造成无限期挂起。
 * 2. 凭据安全与日志静默：
 *    - FFmpeg 默认日志会打印完整 URL（包含用户名和密码凭据）。通过 initialize_ffmpeg 全局将日志等级设为 QUIET 并清空回调，防止凭据泄露到标准输出。
 * 3. 严格的 URL 规范化与参数安全过滤：
 *    - 完整解析并检验 URI 格式，剥离非法空白字符、规范化端口与大小写，避免由于 URL 不规范引起的注入或重复拉流问题。
 * 4. 零拷贝与内存借用视图：
 *    - 读取到的视频帧直接以借用指针封装为 Zhulong_packet_view 供回调读取，避免数据流管道中的多余内存拷贝。
 */

#include "rtsp_input.hpp"

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/log.h>
}

#include <algorithm>
#include <cctype>
#include <cerrno>
#include <memory>
#include <mutex>

namespace zhulong {
namespace {

/**
 * @brief FFmpeg 底层中断回调函数
 *
 * 在 avformat_open_input、avformat_find_stream_info 和 av_read_frame 内部周期性调用。
 * 返回 1 即可让 FFmpeg 立即终止当前阻塞操作并返回 AVERROR_EXIT。
 */
int interrupt(void *opaque) noexcept {
    return static_cast<Cancellation *>(opaque)->interrupted() ? 1 : 0;
}

/**
 * @brief RAII 管理的 AVFormatContext 包装结构
 *
 * 保证在生命周期结束时安全释放上下文。
 * ⚠️ 注意：即使在探测成功或正常结束时，也必须在 avformat_close_input 之前置 cancel.stopped=true，
 * 因为某些 RTSP 服务器在接收 TEARDOWN 命令后不发响应，提前打断可防止关闭时挂死。
 */
struct Input {
    AVFormatContext *context = avformat_alloc_context();
    Cancellation &cancel;

    explicit Input(Cancellation &state) : cancel(state) {
        if (!context) throw std::bad_alloc();
        // 绑定协作式中断控制器
        context->interrupt_callback = {interrupt, &cancel};
    }

    ~Input() {
        // 确保在 TEARDOWN / 关闭网络流时不会因对端停顿而挂起
        cancel.stopped.store(true);
        avformat_close_input(&context);
    }
};

/**
 * @brief RAII 管理的 AVDictionary 字典包装
 */
struct Dictionary {
    AVDictionary *value = nullptr;

    ~Dictionary() {
        av_dict_free(&value);
    }

    void set(const char *key, const std::string &text) {
        if (av_dict_set(&value, key, text.c_str(), 0) < 0) {
            throw std::bad_alloc();
        }
    }
};

/**
 * @brief AVPacket 智能指针自定义删除器
 */
struct PacketDeleter {
    void operator()(AVPacket *packet) const {
        av_packet_free(&packet);
    }
};

/**
 * @brief 校验 FFmpeg 函数执行结果，结合取消状态与超时时间映射异常
 *
 * ⚠️ stream_info 可能在被打断后依然返回非负值（部分元数据成功），
 * 因此必须优先检查 cancel.stopped 与绝对 deadline。
 */
void check(int result, const Cancellation &cancel) {
    if (cancel.stopped.load()) throw Failure{Zhulong_ERR_CANCELLED};
    if (Clock::now() >= cancel.deadline || result == AVERROR(ETIMEDOUT)) {
        throw Failure{Zhulong_ERR_TIMEOUT};
    }
    if (result >= 0) return;
    if (result == AVERROR_EOF) throw Failure{Zhulong_ERR_EOF};
    if (result == AVERROR(ENOMEM)) throw std::bad_alloc();
    throw Failure{Zhulong_ERR_IO};
}

/**
 * @brief 将 FFmpeg 编解码枚举映射为 Zhulong C ABI 编解码枚举
 */
int codec_id(AVCodecID codec) {
    if (codec == AV_CODEC_ID_H264) return Zhulong_CODEC_H264;
    if (codec == AV_CODEC_ID_HEVC) return Zhulong_CODEC_H265;
    throw Failure{Zhulong_ERR_UNSUPPORTED};
}

/** @brief 判断字符是否为空白字符 */
bool ascii_space(unsigned char character) {
    return character == ' ' || character == '\t' || character == '\r' || character == '\n';
}

/** @brief 将字符串转换为小写 */
std::string lower(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char character) {
        return static_cast<char>(std::tolower(character));
    });
    return value;
}

} // namespace

// ============================================================================
//                               参数与 URL 规范化
// ============================================================================

bool Options::operator==(const Options &other) const {
    return transport == other.transport &&
           open_timeout_ms == other.open_timeout_ms &&
           idle_timeout_ms == other.idle_timeout_ms;
}

Options parse_options(const Zhulong_stream_options *options) {
    Options result;
    if (!options) return result;
    if (options->transport != Zhulong_TRANSPORT_TCP && options->transport != Zhulong_TRANSPORT_UDP) {
        throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    }
    result.transport = options->transport;
    if (options->open_timeout_ms) result.open_timeout_ms = options->open_timeout_ms;
    if (options->idle_timeout_ms) result.idle_timeout_ms = options->idle_timeout_ms;
    return result;
}

std::string normalize_rtsp_url(const char *raw) {
    if (!raw) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    std::string url(raw);

    // 剥离两端空白字符
    while (!url.empty() && ascii_space(url.front())) url.erase(url.begin());
    while (!url.empty() && ascii_space(url.back())) url.pop_back();

    // 长度与 scheme 校验
    if (url.size() > 8192 || url.size() < 8 || lower(url.substr(0, 7)) != "rtsp://") {
        throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    }

    // 检查非法字符及校验百分号编码
    for (size_t i = 0; i < url.size(); ++i) {
        const auto character = static_cast<unsigned char>(url[i]);
        if (character <= 32 || character >= 127 || character == '#' || character == '\\') {
            throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        if (character == '%') {
            if (i + 2 >= url.size() ||
                !std::isxdigit(static_cast<unsigned char>(url[i + 1])) ||
                !std::isxdigit(static_cast<unsigned char>(url[i + 2]))) {
                throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
            }
            i += 2;
        }
    }

    // 分离 authority 与路径/查询参数
    auto end = url.find_first_of("/?", 7);
    if (end == std::string::npos) end = url.size();
    std::string authority = url.substr(7, end - 7);

    // 提取 userinfo（用户名:密码）
    std::string userinfo;
    const auto at = authority.find('@');
    if (at != std::string::npos) {
        if (at == 0 || authority.find('@', at + 1) != std::string::npos) {
            throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        userinfo = authority.substr(0, at + 1);
        authority.erase(0, at + 1);
    }

    std::string host;
    std::string port;

    // 解析 IPv6 字面量 [addr]:port 或标准 host:port
    if (!authority.empty() && authority.front() == '[') {
        const auto close = authority.find(']');
        if (close == std::string::npos || close == 1) {
            throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        host = authority.substr(0, close + 1);
        if (close + 1 < authority.size()) {
            if (authority[close + 1] != ':') throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
            port = authority.substr(close + 2);
            if (port.empty()) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        for (size_t i = 1; i < close; ++i) {
            if (!std::isxdigit(static_cast<unsigned char>(host[i])) && host[i] != ':' && host[i] != '.') {
                throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
            }
        }
        if (host.find(':') == std::string::npos) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    } else {
        const auto colon = authority.find(':');
        host = authority.substr(0, colon);
        if (colon != std::string::npos) {
            port = authority.substr(colon + 1);
            if (port.empty()) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        for (unsigned char character : host) {
            if (!std::isalnum(character) && character != '.' && character != '-') {
                throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
            }
        }
    }

    if (host.empty()) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};

    // 规范化端口号：554 为 RTSP 默认端口，省略不拼入以实现 URL 标准归一化
    if (!port.empty()) {
        if (port.size() > 5 || port.find_first_not_of("0123456789") != std::string::npos) {
            throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        const int number = std::stoi(port);
        if (number < 1 || number > 65535) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
        port = number == 554 ? "" : ":" + std::to_string(number);
    }

    return "rtsp://" + userinfo + lower(host) + port + url.substr(end);
}

// ============================================================================
//                               Cancellation 实现
// ============================================================================

void Cancellation::expire_after(uint32_t milliseconds) {
    deadline = Clock::now() + std::chrono::milliseconds(milliseconds);
}

bool Cancellation::interrupted() const noexcept {
    return stopped.load(std::memory_order_relaxed) || Clock::now() >= deadline;
}

Zhulong_video_view Video::borrowed_view() const {
    auto result = view;
    result.extradata = extradata.empty() ? nullptr : extradata.data();
    result.extradata_size = extradata.size();
    return result;
}

void initialize_ffmpeg() {
    static std::once_flag once;
    std::call_once(once, [] {
        // 进程级别静默 FFmpeg 日志，防止 RTSP 认证凭据打入控制台或日志文件
        av_log_set_level(AV_LOG_QUIET);
        av_log_set_callback([](void *, int, const char *, va_list) {});
        // 初始化网络子系统
        if (avformat_network_init() < 0) throw Failure{Zhulong_ERR_IO};
    });
}

// ============================================================================
//                               核心 RTSP 拉流循环
// ============================================================================

Video read_rtsp(const std::string &url, const Options &options, Cancellation &cancel,
                const std::function<void(const Zhulong_packet_view &)> &consume,
                const std::function<void()> &ready) {
    // 设置建连超时
    cancel.expire_after(options.open_timeout_ms);
    check(0, cancel);

    Input input(cancel);
    Dictionary dictionary;
    // 配置 RTSP 参数
    dictionary.set("rtsp_transport", options.transport == Zhulong_TRANSPORT_TCP ? "tcp" : "udp");
    dictionary.set("timeout", std::to_string(static_cast<int64_t>(std::max(options.open_timeout_ms, options.idle_timeout_ms)) * 1000));
    dictionary.set("allowed_media_types", "video");
    // 约束分析流信息的探测时长与字节量，避免在弱网下耗尽超时窗口
    dictionary.set("analyzeduration", "1000000");
    dictionary.set("probesize", "1048576");

    // 1. 打开 RTSP 输入
    check(avformat_open_input(&input.context, url.c_str(), av_find_input_format("rtsp"), &dictionary.value), cancel);

    // 2. 定位最佳视频流轨道
    const int index = av_find_best_stream(input.context, AVMEDIA_TYPE_VIDEO, -1, -1, nullptr, 0);
    if (index < 0) throw Failure{Zhulong_ERR_UNSUPPORTED};

    // 快速前置编解码格式检查（在解析深度流信息前拒绝不支持的格式）
    codec_id(input.context->streams[index]->codecpar->codec_id);

    // 3. 读取流参数信息
    check(avformat_find_stream_info(input.context, nullptr), cancel);

    const auto *stream = input.context->streams[index];
    const auto *parameters = stream->codecpar;

    // 提取并校验视频流元数据
    Video video;
    video.view.codec = codec_id(parameters->codec_id);
    if (parameters->width <= 0 || parameters->height <= 0 ||
        stream->time_base.num <= 0 || stream->time_base.den <= 0) {
        throw Failure{Zhulong_ERR_UNSUPPORTED};
    }
    video.view.width = parameters->width;
    video.view.height = parameters->height;
    video.view.time_base_num = stream->time_base.num;
    video.view.time_base_den = stream->time_base.den;
    video.view.fps_den = 1;
    if (stream->avg_frame_rate.num > 0 && stream->avg_frame_rate.den > 0) {
        video.view.fps_num = stream->avg_frame_rate.num;
        video.view.fps_den = stream->avg_frame_rate.den;
    }
    // 保存 SPS/PPS 参数集（深拷贝）
    if (parameters->extradata_size > 0) {
        video.extradata.assign(parameters->extradata, parameters->extradata + parameters->extradata_size);
    }

    // 若无消费回调，说明仅执行探测（Probe），直接返回元数据
    if (!consume) return video;

    // 通知外部调用方流已就绪（状态变更为 RUNNING）
    if (ready) ready();

    std::unique_ptr<AVPacket, PacketDeleter> packet(av_packet_alloc());
    if (!packet) throw std::bad_alloc();

    // 4. 帧读取循环（使用 idle_timeout_ms 监控两帧之间的间隔）
    cancel.expire_after(options.idle_timeout_ms);
    while (!cancel.stopped.load()) {
        if (cancel.interrupted()) throw Failure{Zhulong_ERR_TIMEOUT};

        const int result = av_read_frame(input.context, packet.get());
        if (result == AVERROR(EAGAIN)) continue;
        check(result, cancel);

        // 仅处理所选视频轨道的有效数据包
        if (packet->stream_index == index && packet->size > 0) {
            // 每收到一个目标视频数据包，刷新空闲超时倒计时
            cancel.expire_after(options.idle_timeout_ms);

            Zhulong_packet_view view{};
            view.codec = video.view.codec;
            view.has_pts = packet->pts != AV_NOPTS_VALUE;
            view.has_dts = packet->dts != AV_NOPTS_VALUE;
            view.pts = view.has_pts ? packet->pts : 0;
            view.dts = view.has_dts ? packet->dts : 0;
            view.time_base_num = stream->time_base.num;
            view.time_base_den = stream->time_base.den;
            view.key_frame = (packet->flags & AV_PKT_FLAG_KEY) != 0;
            view.data = packet->data;
            view.size = static_cast<size_t>(packet->size);

            // 零拷贝借用派发给消费回调
            consume(view);
        }
        av_packet_unref(packet.get());
    }

    return video;
}

} // namespace zhulong
