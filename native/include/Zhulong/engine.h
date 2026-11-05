/**
 * @file engine.h
 * @brief Zhulong Native 引擎通用 C ABI 接口定义
 *
 * 本头文件定义了 Go (通过 CGO) 与 Native C++ 媒体接入引擎交互的稳定 C ABI。
 * 
 * 核心设计原则与边界规范：
 * 1. 稳定的 C ABI：使用纯 C 风格的函数声明与基础类型，禁止 C++ 异常与对象穿越 ABI 边界。
 * 2. 内存所有权明确（谁分配谁释放）：
 *    - 引擎实例句柄 Zhulong_engine_h 由 Zhulong_engine_create 分配，由 Zhulong_engine_destroy 释放。
 *    - 探测结果句柄 Zhulong_probe_result_h 由 Zhulong_engine_probe 分配，由 Zhulong_probe_result_destroy 释放。
 *    - 视图结构体（Zhulong_video_view / Zhulong_packet_view）中的数据指针均为借用指针（Borrowed Pointer），
 *      外部使用者若需长期持有数据必须自行深拷贝，严禁直接释放借用内存。
 * 3. 线程安全与回调排空（Drain）：
 *    - 回调在 Native 工作线程同步触发；回调执行期间严禁调用控制/探测/销毁接口（防止自死锁或重入）。
 *    - 取消订阅（Zhulong_stream_unsubscribe）具有同步排空语义：返回后保证该订阅无任何回调在执行或即将触发。
 */

#ifndef ZHULONG_ENGINE_H
#define ZHULONG_ENGINE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/** @brief Native 引擎实例的不透明句柄类型 */
typedef struct Zhulong_engine_t *Zhulong_engine_h;

/** @brief 独立流探测（Probe）结果的不透明句柄类型 */
typedef struct Zhulong_probe_result_t *Zhulong_probe_result_h;

/** @brief 通用错误与状态码类型，0 表示成功，负数表示错误 */
typedef int32_t Zhulong_status_t;

/** @brief 物理流唯一标识 ID（> 0） */
typedef uint64_t Zhulong_stream_id;

/** @brief 数据包订阅唯一标识 ID（> 0） */
typedef uint64_t Zhulong_subscription_id;

/**
 * @brief 错误码枚举
 */
enum {
    Zhulong_OK                    = 0,    /**< 操作成功 */
    Zhulong_ERR_INVALID_ARGUMENT  = -1,   /**< 参数无效（如空指针、非法 URL、超出范围的数值等） */
    Zhulong_ERR_OUT_OF_MEMORY     = -2,   /**< 内存分配失败（OOM） */
    Zhulong_ERR_UNSUPPORTED       = -3,   /**< 不支持的编解码格式、协议或容器格式 */
    Zhulong_ERR_NOT_FOUND         = -4,   /**< 指定的流、订阅或资源不存在 */
    Zhulong_ERR_DUPLICATE         = -5,   /**< 资源或消费者重复注册 */
    Zhulong_ERR_CONFIG_CONFLICT   = -6,   /**< 请求的拉流参数与已存在的物理流配置冲突 */
    Zhulong_ERR_TIMEOUT           = -7,   /**< 操作超时（网络建立超时或空闲读超时） */
    Zhulong_ERR_CALLBACK_CONTEXT  = -8,   /**< 禁止在数据包回调的上下文中调用控制/销毁 API（防自死锁） */
    Zhulong_ERR_NOT_RUNNING       = -9,   /**< 引擎处于停止或未启动状态 */
    Zhulong_ERR_BUSY              = -10,  /**< 资源正忙（例如仍存在活跃订阅时尝试释放消费者） */
    Zhulong_ERR_IO                = -11,  /**< 底层网络或 I/O 通信错误 */
    Zhulong_ERR_CANCELLED         = -12,  /**< 操作被显式取消或引擎停止 */
    Zhulong_ERR_EOF               = -13,  /**< 流遇到文件末尾或对端已关闭 */
    Zhulong_ERR_BUFFER_TOO_SMALL  = -14,  /**< 提供的缓冲区容量不足 */
    Zhulong_ERR_HARDWARE_UNAVAILABLE = -15, /**< 硬件加速器不可用且禁止软解 */
    Zhulong_ERR_CAPACITY_EXCEEDED    = -16, /**< 并发解码路数已达系统声明的上限 */
    Zhulong_ERR_NOT_READY         = -17,  /**< 资源尚未就绪（如视图已登记但还没有解码帧尺寸） */
    Zhulong_ERR_INTERNAL          = -99   /**< 引擎内部未知未捕获错误 */
};

/**
 * @brief RTSP 底层传输协议枚举
 */
enum {
    Zhulong_TRANSPORT_TCP = 0,            /**< TCP 传输（RTP over RTSP，默认选项） */
    Zhulong_TRANSPORT_UDP = 1             /**< UDP 传输（标准 RTP/UDP） */
};

/**
 * @brief 消费者类型枚举（用于流池复用与优先级区分）
 */
enum {
    Zhulong_CONSUMER_PREVIEW   = 1,       /**< 实时预览消费者（允许订阅原始视频数据包） */
    Zhulong_CONSUMER_RECORDING = 2,       /**< 录像存储消费者 */
    Zhulong_CONSUMER_AI        = 3        /**< AI 异构推理消费者 */
};

/**
 * @brief 视频/音频编解码器枚举
 */
enum {
    Zhulong_CODEC_H264 = 1,               /**< H.264 / AVC */
    Zhulong_CODEC_H265 = 2,               /**< H.265 / HEVC */
    Zhulong_CODEC_AAC  = 16,              /**< AAC (raw AU) */
    Zhulong_CODEC_PCMA = 17,              /**< G.711 A-law */
    Zhulong_CODEC_PCMU = 18               /**< G.711 mu-law */
};

/**
 * @brief 物理流运行状态枚举
 */
enum {
    Zhulong_STREAM_STARTING = 0,          /**< 流正在建立连接与解析格式 */
    Zhulong_STREAM_RUNNING  = 1,          /**< 流已正常建立并持续接收数据 */
    Zhulong_STREAM_FAILED   = 2           /**< 流已失败或遇到不可恢复错误 */
};

/**
 * @brief 拉流配置选项结构体
 */
typedef struct {
    int32_t transport;           /**< 传输层协议：Zhulong_TRANSPORT_TCP 或 Zhulong_TRANSPORT_UDP，同 URL 禁止混用 */
    uint32_t open_timeout_ms;    /**< 连接与解析流信息的超时时间（毫秒），传 0 则默认为 5000ms */
    uint32_t idle_timeout_ms;    /**< 读取数据包的空闲超时时间（毫秒），传 0 则默认为 5000ms；仅选中的有效视频包刷新此计时器 */
} Zhulong_stream_options;

/** @brief 解码后端选择枚举 */
typedef enum {
    Zhulong_DECODE_BACKEND_AUTO   = 0, /**< 优先硬件解码，不可用时降级为软解（开发环境默认） */
    Zhulong_DECODE_BACKEND_MPP    = 1, /**< 强制 Rockchip MPP 硬件解码，不可用时拒绝 */
    Zhulong_DECODE_BACKEND_FFMPEG = 2  /**< 强制 FFmpeg 软解（仅供调试或无硬件环境） */
} Zhulong_decode_backend_t;

/** @brief 全局解码策略配置结构体 */
typedef struct {
    int32_t backend;                 /**< Zhulong_decode_backend_t 枚举 */
    int32_t allow_software_fallback; /**< 0: 严禁软解降级 (fail-closed); 1: 允许软解降级 */
    int32_t max_sessions;            /**< 最大并发物理解码流路数（>0 生效；0 为不限） */
} Zhulong_decode_policy_t;

/** @brief 解码器运行态统计结构体（16字节紧凑对齐，跨平台 ABI 稳定） */
typedef struct {
    int32_t active_sessions;         /**< 当前活跃的物理解码流数 */
    int32_t max_sessions;            /**< 允许的最大并发物理解码流数 */
    int32_t rejected_requests;       /**< 历史累计被准入拒绝或降级拒绝的请求次数 */
    int32_t hardware_available;      /**< 硬件解码器底层是否就绪（1=可用, 0=不可用） */
} Zhulong_decode_stats_t;

/**
 * @brief 视频流元数据视图（借用结构，由探测结果借出）
 *
 * 内存所有权约定：
 *   - extradata 指针借用自底层 FFmpeg 内存，仅在对应的 probe_result 销毁前有效。
 *   - 外部调用者严禁修改或 free 此处指针。
 */
typedef struct {
    int32_t codec;               /**< 视频编码格式（Zhulong_CODEC_H264 或 Zhulong_CODEC_H265） */
    int32_t width;               /**< 视频宽度（像素） */
    int32_t height;              /**< 视频高度（像素） */
    int32_t fps_num;             /**< 帧率分子（未知帧率时为 0，分母为 1，绝不随意揣测） */
    int32_t fps_den;             /**< 帧率分母 */
    int32_t time_base_num;       /**< 时间基分子（Timebase Numerator） */
    int32_t time_base_den;       /**< 时间基分母（Timebase Denominator） */
    const uint8_t *extradata;    /**< 视频头/参数集数据（SPS/PPS/VPS，原始 FFmpeg 表示，不保证 Annex B 格式） */
    size_t extradata_size;       /**< extradata 的字节长度，可为 0 */
} Zhulong_video_view;

/**
 * @brief 视频数据包视图（借用结构，回调期间同步传递）
 *
 * 内存所有权与生命周期约定：
 *   - data 指针仅在回调函数同步调用的执行周期内有效！
 *   - 若回调接收方需保留数据包供异步处理，必须在回调返回前完成独立内存分配并进行深拷贝（Deep Copy）。
 *   - 时间戳 pts / dts 为源流时间基下的原生滴答数（Ticks），而非系统墙上时钟或毫秒。
 *   - v3 起额外提供微秒单位的 pts_us / dts_us；pts_us 保留显示顺序，允许 B 帧重排，
 *     dts_us 按解码顺序单调（源缺失 DTS 时为合成值）。源流 ticks / time_base 继续透传，
 *     供 AI 解码等需要原始时基的消费者使用。
 */
typedef struct {
    int32_t codec;               /**< 数据包编解码类型 */
    int64_t pts;                 /**< 显示时间戳（Presentation Timestamp，单位见 time_base） */
    int64_t dts;                 /**< 解码时间戳（Decoding Timestamp，单位见 time_base） */
    int32_t has_pts;             /**< 是否存在有效的 PTS（1 为有效，0 为缺失） */
    int32_t has_dts;             /**< 是否存在有效的 DTS（1 为有效，0 为缺失） */
    int32_t time_base_num;       /**< 流时间基分子 */
    int32_t time_base_den;       /**< 流时间基分母 */
    int32_t key_frame;           /**< 是否为关键帧 / IDR 帧（1 为关键帧，0 为非关键帧） */
    const uint8_t *data;         /**< 数据负载首地址（借用指针） */
    size_t size;                 /**< 数据负载长度（字节数） */

    /* ---------------- v3 追加：预览路径归一化字段（原有偏移不变） ---------------- */

    int64_t pts_us;              /**< 显示时间戳微秒值；保留 B 帧重排，不保证按包序单调 */
    int64_t dts_us;              /**< 解码时间戳微秒值；按包序单调，缺失源 DTS 时使用合成值 */
    int32_t frame_class;         /**< 帧类型，取值见 Zhulong_FRAME_*；决定接收方可否安全丢弃 */
    uint32_t gop_id;             /**< GOP 序号：每遇到关键帧或时间轴跳变时自增，跨值即代表可重建解码器 */
    uint32_t pkt_flags;          /**< 包属性位，取值见 ZHULONG_PKT_FLAG_* */

    /* ---------------- v5 追加：双轨按需音频字段 ---------------- */
    uint32_t track_type;         /**< 轨道类型：ZHULONG_TRACK_VIDEO(0) 或 ZHULONG_TRACK_AUDIO(1) */
    uint32_t reserved0;          /**< 预留对齐填充，恒为 0 */
    uint32_t reserved1;          /**< 预留对齐填充，恒为 0 */
    uint32_t reserved2;          /**< 预留对齐填充，恒为 0 */
} Zhulong_packet_view;

/** @brief 轨道类型枚举（v5） */
enum {
    ZHULONG_TRACK_VIDEO = 0,
    ZHULONG_TRACK_AUDIO = 1
};

/** @brief 订阅轨道掩码（v5） */
enum {
    ZHULONG_TRACK_MASK_VIDEO = 1u << 0,
    ZHULONG_TRACK_MASK_AUDIO = 1u << 1
};

/**
 * @brief 预览帧类型枚举（v3）
 *
 * 语义约定：
 *   - 接收方仅可无条件丢弃 Zhulong_FRAME_NON_REFERENCE；
 *   - 丢弃 Zhulong_FRAME_PREDICTIVE 会破坏后续帧的参考链，必须整段丢弃至下一个 IDR；
 *   - 引擎在无法可靠判定时一律归类为 PREDICTIVE（保守策略，绝不误判为可丢）。
 */
enum {
    Zhulong_FRAME_UNKNOWN       = 0, /**< 未分类（旧版原生库或非视频包） */
    Zhulong_FRAME_IDR           = 1, /**< 关键帧 / 随机接入点 */
    Zhulong_FRAME_PREDICTIVE    = 2, /**< 参考帧，不可单独丢弃 */
    Zhulong_FRAME_NON_REFERENCE = 3  /**< 非参考帧，可安全丢弃而不影响后续解码 */
};

/** @brief pts_us 由 dts 合成（源流缺失 PTS） */
#define ZHULONG_PKT_FLAG_TIMESTAMP_SYNTHETIC 0x0001u
/** @brief 引擎已为本帧前置注入参数集（VPS/SPS/PPS），接收方无需再等待 */
#define ZHULONG_PKT_FLAG_PARAM_SETS_INJECTED 0x0002u
/** @brief 源流时间轴发生跳变（典型为相机或 RTSP 会话重建），接收方必须重置解码器 */
#define ZHULONG_PKT_FLAG_DISCONTINUITY       0x0004u
/** @brief 源流缺失 DTS，dts_us 是用于诊断/背压的合成解码时钟 */
#define ZHULONG_PKT_FLAG_DTS_SYNTHETIC        0x0008u

/**
 * @brief 流状态信息结构体
 *
 * 字段说明：
 *   - state: 物理流当前运行状态，取值为 Zhulong_STREAM_STARTING（建连与格式解析中）、
 *     Zhulong_STREAM_RUNNING（正常运行持续拉流）或 Zhulong_STREAM_FAILED（遇到不可恢复错误已终止）。
 *   - error: 错误码。当 state 为 Zhulong_STREAM_FAILED 时记录导致失败的具体错误码
 *     （如 Zhulong_ERR_IO、Zhulong_ERR_TIMEOUT 等）；流正常运行或处于启动中状态时为 Zhulong_OK。
 */
typedef struct {
    int32_t state;               /**< 流当前运行状态（Zhulong_STREAM_STARTING / RUNNING / FAILED） */
    Zhulong_status_t error;      /**< 安全的定长错误码，避免向上暴露未经脱敏的 FFmpeg 错误文本或 URL 凭据 */
} Zhulong_stream_status;

/**
 * @brief 单个算法 Runner 客户端的帧投递统计（FrameBroker 视角，只读快照）
 *
 * 用途：把「某路 AI 分析流上的帧到底投给了谁、丢了多少」暴露给宿主。
 * 这些计数器存在于 Native FrameBroker 内部，之前没有任何 ABI 出口，
 * 导致「算法包静默丢帧」在 Go 层完全不可见，只能靠 CPU 占用猜。
 *
 * 字段说明（均为自 Broker start 起的累计值，除非另有说明）：
 * - `in_flight`：当前已下发但尚未 ACK 的帧数，瞬时值；
 * - `max_in_flight`：该客户端的在途配额上限，瞬时值，即背压阈值；
 * - `delivered_frames`：累计成功下发且被消费的帧数；
 * - `dropped_frames`：因背压（in-flight 满）或 socket 发送失败累计丢弃的帧数。
 */
typedef struct {
    uint32_t client_id;        /**< Broker 分配的客户端 ID（仅在单次 Broker 生命周期内唯一） */
    uint32_t in_flight;        /**< 当前在途未归还租约数 */
    uint32_t max_in_flight;    /**< 该客户端的在途配额上限 */
    uint32_t reserved_pad;     /**< 对齐填充，恒为 0；不得赋予语义 */
    uint64_t delivered_frames; /**< 累计成功投递帧数 */
    uint64_t dropped_frames;   /**< 累计因背压/发送失败丢弃的帧数 */
    char name[32];             /**< 握手时上报的客户端名（`--name`），未命名时为空串 */
} Zhulong_broker_client_stats;

/**
 * @brief 视频数据包回调函数原型
 *
 * 约定与安全要求：
 *   - 本回调由 Native 拉流 Worker 线程同步调用。
 *   - 回调操作必须是快速、有界且非阻塞的，严禁抛出 C++ 异常。
 *   - 严禁在回调内部调用引擎的任何控制接口（如 acquire/release/probe/stop/destroy），否则会返回 Zhulong_ERR_CALLBACK_CONTEXT。
 *   - token 为调用方注册的不透明整数，在 Go 中可配合 cgo.Handle 使用，严禁直接传递裸 Go 指针。
 *
 * @param token 注册订阅时传入的用户上下文标识
 * @param packet 数据包视图借用指针
 */
typedef void (*Zhulong_packet_callback)(uintptr_t token, const Zhulong_packet_view *packet);

/**
 * @brief AI 推理结果事件到达时的回调函数指针
 *
 * @param token 注册订阅时传入的用户上下文标识（如 cgo.Handle）
 * @param stream 物理流 ID
 * @param json_payload JSON 格式的推理结果借用数据指针（回调返回后失效，外部若需保留必须深拷贝）
 * @param payload_len 载荷字节长度
 */
typedef void (*Zhulong_ai_result_callback)(uintptr_t token, Zhulong_stream_id stream,
    const char *json_payload, size_t payload_len);

/** @brief Native log severity values; independent of host logger values. */
enum {
    Zhulong_LOG_DEBUG = 0,
    Zhulong_LOG_INFO = 1,
    Zhulong_LOG_WARN = 2,
    Zhulong_LOG_ERROR = 3
};

/**
 * @brief Native log callback. Module and text are borrowed for the call only.
 * module must be 1..128 bytes; text must be 0..2048 bytes.
 */
typedef void (*Zhulong_log_callback)(uintptr_t token, int32_t level, uint64_t stream_id,
    const char *module, size_t module_len, const char *text, size_t text_len);

/* ========================================================================== */
/*                             引擎生命周期管理接口                              */
/* ========================================================================== */

/**
 * @brief 当前 C ABI 版本号（v6 为 AI 流分析视图 / 任务级裁剪支持）
 *
 * 演进约定：ABI 只允许在结构体**尾部追加**字段，绝不调整已有字段偏移或语义。
 * Go 侧在初始化后必须调用 Zhulong_engine_get_abi_version 校验，头文件与静态库不一致时
 * 直接拒绝启动，避免旧归档被语言层静默复用引发的内存越界。
 */
#define ZHULONG_ABI_VERSION 6

/**
 * @brief 查询原生静态库编译时对应的 ABI 版本
 *
 * 用途：Make / wrapper 已对 Engine 归档做内容寻址，但交叉产物、旧 build 目录或手工链接
 * 仍可能把上一次的头文件和新一次的归档混在一起。本接口让这种不一致当场暴露。
 *
 * @param[out] out_version 输出 ABI 版本号
 * @return Zhulong_status_t Zhulong_OK 成功；Zhulong_ERR_INVALID_ARGUMENT 空指针
 */
Zhulong_status_t Zhulong_engine_get_abi_version(uint32_t *out_version);

/**
 * @brief 创建并分配 Native 引擎实例
 *
 * @param[out] out_engine 输出参数，接收创建成功后的引擎句柄指针
 * @return Zhulong_status_t Zhulong_OK 成功；Zhulong_ERR_INVALID_ARGUMENT 参数空；Zhulong_ERR_OUT_OF_MEMORY 内存不足
 */
Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine);

/**
 * @brief Register or clear the native logging callback.
 * Registration is permitted only before any engine handle is created; clear it
 * only after all native producers have stopped and all engine handles destroyed.
 * Pass callback=NULL and token=0 to clear the callback.
 */
Zhulong_status_t Zhulong_set_log_callback(Zhulong_log_callback callback, uintptr_t token);

/**
 * @brief 启动 Native 引擎及内部清理/调度线程
 *
 * 具备幂等性：重复对运行中的引擎调用 start 直接返回 Zhulong_OK。
 * 若引擎此前曾被 stop 停止，再次调用 start 将以全新空的流池重新启动运行。
 *
 * @param engine 引擎句柄
 * @return Zhulong_status_t Zhulong_OK 成功；Zhulong_ERR_INVALID_ARGUMENT 句柄为空
 */
Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine);

/**
 * @brief 停止 Native 引擎
 *
 * 取消所有活跃拉流和探测、回收 Worker 线程、排空回调，并使所有流 ID 与订阅 ID 失效。
 * 具备幂等性：重复对已停止或未启动的引擎调用 stop 直接返回 Zhulong_OK。
 * 停止后若再次调用 start，将以全新空的流池重新运行。
 *
 * ⚠️ 严禁在任何 Native 数据包回调或 AI 结果回调内部调用此接口（防自死锁）。
 *
 * @param engine 引擎句柄
 * @return Zhulong_status_t Zhulong_OK 成功；Zhulong_ERR_INVALID_ARGUMENT 句柄为空
 */
Zhulong_status_t Zhulong_engine_stop(Zhulong_engine_h engine);

/**
 * @brief 销毁 Native 引擎并释放其所有 C++ 堆内存
 *
 * 调用方必须自行确保销毁调用与其他所有操作（包括 probe）串行化执行。
 * 严禁在任何 Native 回调中调用此函数。传入 NULL 指针是安全的操作。
 *
 * @param engine 待销毁的引擎句柄
 */
void Zhulong_engine_destroy(Zhulong_engine_h engine);

/**
 * @brief 配置引擎全局解码策略
 *
 * 必须在 Zhulong_engine_create 之后、拉流之前调用；支持在运行期间动态更新策略。
 *
 * @param engine 引擎句柄
 * @param policy 解码策略指针
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_engine_configure_decode(Zhulong_engine_h engine, const Zhulong_decode_policy_t *policy);

/**
 * @brief 查询引擎解码器运行态统计信息
 *
 * @param engine 引擎句柄
 * @param[out] out_stats 统计信息输出缓冲区
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_engine_get_decode_stats(Zhulong_engine_h engine, Zhulong_decode_stats_t *out_stats);

/* ========================================================================== */
/*                             流池与流生命周期管理                              */
/* ========================================================================== */

/**
 * @brief 获取或复用物理 RTSP 流
 *
 * URL 规范要求：
 *   - 必须是绝对 rtsp:// URI，ASCII 主机名（或带方括号的 IPv6 地址），带百分号编码的用户凭据/路径/查询参数。
 *   - 拒绝未转义空白字符、片段（#）以及歧义的 @ 字符。
 *   - 函数内部在返回前会对 URL 与 options 进行规范化并深拷贝。
 *
 * 复用与引用计数：
 *   - 相同 URL 且相同配置的流将被复用。
 *   - consumer_id 必须非零且在当前物理流的各消费者中唯一。
 *
 * @param engine 引擎句柄
 * @param url RTSP 地址字符串
 * @param options 拉流配置（传 NULL 表示使用默认配置）
 * @param consumer_id 消费者业务 ID
 * @param consumer_kind 消费者类型（Zhulong_CONSUMER_PREVIEW / RECORDING / AI）
 * @param[out] out_stream 输出分配或复用的流 ID
 * @return Zhulong_status_t 操作状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法（engine/url/out_stream 为空指针、URL 格式非法、consumer_id 为 0）；
 *         Zhulong_ERR_DUPLICATE consumer_id 在当前流中已存在（重复注册）；
 *         Zhulong_ERR_CONFIG_CONFLICT 请求的 URL 与已存在的流相同但拉流选项冲突（如 transport 不一致）；
 *         Zhulong_ERR_NOT_RUNNING 引擎处于停止或未启动状态；
 *         Zhulong_ERR_OUT_OF_MEMORY 内存不足
 */
Zhulong_status_t Zhulong_stream_acquire(Zhulong_engine_h engine, const char *url,
    const Zhulong_stream_options *options, uint64_t consumer_id, int32_t consumer_kind,
    Zhulong_stream_id *out_stream);

/**
 * @brief 释放对物理流的引用
 *
 * 约定与宽限期（Grace Period）：
 *   - 释放消费者前，必须先取消该消费者名下的所有数据包订阅（unsubscribe）。
 *   - 当物理流的最后一个消费者释放时，流不会立即关闭，而是进入 8 秒的宽限期；
 *     若宽限期内有新的 acquire 复用该流，将取消过期倒计时继续复用底层连接。
 *
 * @param engine 引擎句柄
 * @param stream 流 ID
 * @param consumer_id 待释放的消费者 ID
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_stream_release(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t consumer_id);

/**
 * @brief 获取指定流的当前运行状态与错误信息
 *
 * @param engine 引擎句柄
 * @param stream 流 ID
 * @param[out] out_status 输出状态结构体指针
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_stream_get_status(Zhulong_engine_h engine, Zhulong_stream_id stream,
    Zhulong_stream_status *out_status);

/**
 * @brief 查询指定流关联的 FrameBroker Unix Domain Socket 路径
 *
 * 调用前提：该流已通过 Zhulong_stream_acquire 以 Zhulong_CONSUMER_AI 获取，
 * 且至少有一个 AI 消费者仍在借用该流。
 *
 * @param engine    引擎句柄
 * @param stream    物理流 ID
 * @param out_path  输出路径缓冲区（调用方分配）
 * @param inout_len 输入为缓冲区容量；输出为实际写入的路径字符串长度（包含末尾 '\\0'）
 * @return Zhulong_status_t 操作状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法（空指针、容量为 0）；
 *         Zhulong_ERR_NOT_FOUND 流不存在或该流无 AI 消费者（未创建 FrameBroker）；
 *         Zhulong_ERR_BUFFER_TOO_SMALL 缓冲不足（inout_len 回填所需大小）；
 *         Zhulong_ERR_NOT_RUNNING 引擎未处于运行状态
 */
Zhulong_status_t Zhulong_stream_get_broker_path(Zhulong_engine_h engine,
    Zhulong_stream_id stream, char *out_path, size_t *inout_len);

/**
 * @brief 查询指定流 FrameBroker 的客户端投递统计快照
 *
 * 调用前提：该流已通过 `Zhulong_stream_acquire` 以 `Zhulong_CONSUMER_AI` 获取，
 * 且 FrameBroker 仍在运行。
 *
 * ⚠️ 契约刻意**不**复用 `Zhulong_stream_get_broker_path` 的「容量不足就报
 *    `BUFFER_TOO_SMALL`」模式：客户端集合是动态的，两次查询之间可能已经变化。
 *    本接口改为「写入不超过 `capacity` 条，并把**真实总数**写入 `out_count`」——
 *    调用方看到 `*out_count > capacity` 就知道结果被截断，一次调用即可完成。
 *
 * @param engine       引擎句柄
 * @param stream       物理流 ID
 * @param out_clients  调用方提供的数组；`capacity == 0` 时允许传 NULL（仅查询数量）
 * @param capacity     `out_clients` 的数组容量（可容纳多少条）
 * @param[out] out_count 输出当前客户端**总数**（可能大于 capacity，表示已截断）
 * @return Zhulong_status_t 操作状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法（空 out_count，或 capacity > 0 但
 *         out_clients 为空）；
 *         Zhulong_ERR_NOT_FOUND 流不存在或该流无 AI 消费者（未创建 FrameBroker）；
 *         Zhulong_ERR_NOT_RUNNING 引擎未处于运行状态
 */
Zhulong_status_t Zhulong_stream_get_broker_clients(Zhulong_engine_h engine,
    Zhulong_stream_id stream, Zhulong_broker_client_stats *out_clients,
    uint32_t capacity, uint32_t *out_count);

/**
 * @brief 动态设置指定流的帧发布节流帧率 (按 D7 准则由关联任务的 max(sample_fps) 驱动)
 *
 * @param engine         引擎句柄
 * @param stream         物理流 ID
 * @param max_sample_fps 最大采样率（0 表示不节流，按解码原帧率发布）
 * @return Zhulong_status_t 操作状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法；
 *         Zhulong_ERR_NOT_FOUND 流不存在或该流无 AI 消费者（未激活发布通道）；
 *         Zhulong_ERR_NOT_RUNNING 引擎未处于运行状态
 */
Zhulong_status_t Zhulong_stream_set_publish_rate(Zhulong_engine_h engine,
    Zhulong_stream_id stream, uint32_t max_sample_fps);

/* ========================================================================== */
/*                        AI 流分析视图（任务级裁剪，v6）                        */
/* ========================================================================== */

/** @brief 归一化窗口矩形：严格 [0,1] 且 x1 < x2、y1 < y2；{0,0,1,1} 表示全幅/未设置。 */
typedef struct {
    float x1;
    float y1;
    float x2;
    float y2;
} Zhulong_view_rect;

/** @brief 引擎回报的生效窗口：>= effective_since_ms 的帧使用 rect。 */
typedef struct {
    Zhulong_view_rect rect;          /**< 生效 E（按解码帧尺寸归一化） */
    int64_t effective_since_ms;      /**< 宿主 UTC 毫秒；0 表示尚未发生过窗口切换 */
} Zhulong_view_state;

/**
 * @brief 设置指定 AI 流的分析视图（任务级裁剪窗口）。
 *
 * 语义：
 *  - **非 acquire**：流不存在返回 `Zhulong_ERR_NOT_FOUND`，绝不顺手拉起拉流；
 *  - `view == NULL` 或 `{0,0,1,1}` 表示清除，**永远成功**（即使还没有解码帧）；
 *  - 非空窗口但还拿不到解码帧尺寸 → `Zhulong_ERR_NOT_READY`：配置已保存、视图暂不生效，
 *    首个解码帧到达后由引擎自动计算并推进 `effective_since_ms`；
 *  - 非法坐标（非有限、越界、退化）→ `Zhulong_ERR_INVALID_ARGUMENT`。
 *    ⚠️ 最小边长（Go 侧 MinCropSide）**不在 C 侧校验**：0.02 过 float32 会舍入成
 *    0.0199999996，边界值会被误拒；阈值权威只在宿主 Go 层。
 *  - 幂等：同值重复设置不改变 E，也不推进 `effective_since_ms`。
 *
 * @param engine        引擎句柄
 * @param stream        物理流 ID（必须是 AI 消费流）
 * @param view          目标窗口；NULL 表示清除
 * @param[out] out_effective 可为 NULL；成功时返回本次生效的 E 与切换点
 * @return Zhulong_status_t
 */
Zhulong_status_t Zhulong_stream_set_view(Zhulong_engine_h engine, Zhulong_stream_id stream,
    const Zhulong_view_rect *view, Zhulong_view_state *out_effective);

/**
 * @brief 查询当前生效 E 与生效起点。
 *
 * 未设置视图或尚无解码帧时返回 `rect={0,0,1,1}`、`effective_since_ms=0`。
 * 流不存在返回 `Zhulong_ERR_NOT_FOUND`；本接口只读，不会拉起拉流。
 */
Zhulong_status_t Zhulong_stream_get_view(Zhulong_engine_h engine, Zhulong_stream_id stream,
    Zhulong_view_state *out_effective);

/**
 * @brief 音频流元数据视图（v5）
 *
 * 由调用方分配结构体并提供 extradata 缓冲区，ABI 调用期间深拷贝复制，不返回内部借用指针。
 * 若 extradata 缓冲区容量不足，返回 Zhulong_ERR_BUFFER_TOO_SMALL，并在 extradata_size 中写入所需字节数。
 */
typedef struct {
    int32_t  has_audio;           /**< 是否包含有效音频轨（1 为有，0 为无） */
    int32_t  codec;               /**< 音频编码格式：Zhulong_CODEC_AAC / PCMA / PCMU */
    int32_t  sample_rate;         /**< 采样率（Hz），如 8000, 16000, 44100, 48000 */
    int32_t  channels;            /**< 声道数，1 为单声道，2 为立体声 */
    uint8_t *extradata;           /**< 调用方提供的 extradata 缓冲区；AAC 为 AudioSpecificConfig (ASC) */
    size_t   extradata_capacity;  /**< extradata 缓冲区容量（字节） */
    size_t   extradata_size;      /**< 实际写入字节数（或容量不足时所需的字节数） */
} Zhulong_audio_view;

/**
 * @brief 查询指定物理流的音频流元数据（v5）
 *
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param[in,out] out_view 指向调用方提供的音频元数据视图结构体
 * @return Zhulong_status_t Zhulong_OK 成功；
 *                          Zhulong_ERR_INVALID_ARGUMENT 参数空；
 *                          Zhulong_ERR_BUFFER_TOO_SMALL extradata 缓冲区容量不足；
 *                          Zhulong_ERR_NOT_FOUND 流不存在；
 *                          Zhulong_ERR_NOT_RUNNING 流未在运行中
 */
Zhulong_status_t Zhulong_stream_audio_info(Zhulong_engine_h engine, Zhulong_stream_id stream,
                                           Zhulong_audio_view *out_view);

/**
 * @brief 按轨订阅流的数据包通道（v5）
 *
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param preview_consumer_id 预览消费者 ID（> 0）
 * @param track_mask 订阅轨道掩码（ZHULONG_TRACK_MASK_VIDEO, ZHULONG_TRACK_MASK_AUDIO 或其按位或）
 * @param callback 接收数据包的回调函数
 * @param token 传递给回调函数的上下文令牌
 * @param[out] out_subscription 输出订阅 ID
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_stream_subscribe_track(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t preview_consumer_id, uint32_t track_mask,
    Zhulong_packet_callback callback, uintptr_t token,
    Zhulong_subscription_id *out_subscription);

/**
 * @brief 订阅物理流的实时数据包派发（保持仅视频订阅语义）
 *
 * @param engine 引擎句柄
 * @param stream 流 ID
 * @param preview_consumer_id 预览类型消费者 ID（必须已通过 acquire 获取过）
 * @param callback 数据包到达时的回调函数指针
 * @param token 传递给回调函数的上下文令牌
 * @param[out] out_subscription 输出订阅 ID
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_stream_subscribe(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t preview_consumer_id, Zhulong_packet_callback callback, uintptr_t token,
    Zhulong_subscription_id *out_subscription);

/**
 * @brief 取消数据包订阅（同步排空语义）
 *
 * 同步排空保证：
 *   - 本函数成功返回后，保证所有正在执行此 token 的回调已彻底返回，且未来绝不会再触发。
 *   - Go 侧在调用本接口成功后，即可安全回收或解除关联的 cgo.Handle。
 *
 * @param engine 引擎句柄
 * @param stream 流 ID
 * @param subscription 待取消的订阅 ID
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_stream_unsubscribe(Zhulong_engine_h engine, Zhulong_stream_id stream,
    Zhulong_subscription_id subscription);

/**
 * @brief 抓拍请求中允许携带的最大裁剪框数量
 *
 * 上限存在的原因：一次抓拍要为每个框单独编码一张 JPEG，而 MJPEG 编码是有
 * 成本的同步操作。8 个框已经覆盖「一帧内多个告警目标」的实际场景，超出请分批调用。
 */
#define ZHULONG_SNAPSHOT_MAX_BOXES 8

/**
 * @brief 单张抓拍图（全幅或某个裁剪框）
 *
 * 内存所有权（⚠️ 谁分配谁释放）：
 *   - data 由 Native 侧分配，由 Zhulong_snapshot_destroy 统一释放；
 *   - 调用方若需长期持有，必须在 destroy 之前自行深拷贝；
 *   - 严禁对 data 调用 free/realloc，也严禁把 Go 堆指针交给本结构。
 *   - data 指向**完整可独立解码**的 JPEG 字节流（含 SOI/EOI），不是裸像素。
 */
typedef struct {
    int32_t      box_index; /**< -1 表示全幅图；>=0 表示对应请求 boxes[box_index] 的裁剪图 */
    const uint8_t *data;    /**< JPEG 字节流首地址（借用，随 result 句柄失效） */
    size_t       size;      /**< JPEG 字节流长度 */
    int32_t      width;     /**< 该图实际像素宽度 */
    int32_t      height;    /**< 该图实际像素高度 */
} Zhulong_snapshot_image;

/**
 * @brief 抓拍请求
 *
 * 坐标约定：boxes 中的每个框为**归一化** [x1, y1, x2, y2] ∈ [0.0, 1.0]，
 * 与 zhulong_bbox 的坐标系完全一致，调用方无需做像素换算。
 *
 * 返回顺序约定：结果中恒有且仅有一张 box_index = -1 的全幅图，其后按
 * box_index 升序排列各裁剪图。某个框因越界或退化（宽/高不足 2 像素）
 * 而被跳过时，该索引不会出现在结果中——调用方必须按 box_index 索引，
 * 不得假设「第 i 张裁剪图对应 boxes[i]」。
 */
typedef struct {
    int32_t quality;      /**< JPEG 质量 1..100（越界时收敛到 [1,100]，传 0 使用默认 85） */
    int32_t box_count;    /**< 裁剪框数量，范围 [0, ZHULONG_SNAPSHOT_MAX_BOXES]；0 表示只出全幅图 */
    const float *boxes;   /**< 长度为 box_count * 4 的归一化坐标数组；box_count 为 0 时可为 NULL */
    int64_t target_pts;   /**< 目标帧 PTS 时间戳（单位为源流时间基下的原生滴答数 Ticks）；若 <= 0 则抓取当前最新帧，若 > 0 则优先在历史环形缓存中精确匹配 */
} Zhulong_snapshot_request;

/** @brief 抓拍结果的不透明句柄类型，由 Zhulong_stream_snapshot 分配 */
typedef struct Zhulong_snapshot_result_t *Zhulong_snapshot_h;

/**
 * @brief 对指定流的 AI 解码会话执行抓拍（全幅 + 可选裁剪），输出 JPEG
 *
 * 语义与限制（⚠️ 请完整阅读后再使用）：
 *   - **帧来源**：若 request->target_pts > 0，优先在 Native 历史环形缓存中精确
 *     匹配对应 PTS 的帧；若历史已淘汰或 target_pts <= 0，退回当前最新的一帧。
 *   - **持帧上限与缓冲预算**：Native 侧为历史抓拍保留固定 4 帧借用引用（实测
 *     推理结果回传延迟约 1~4 帧）。在 RK3568 硬解 720p（10 缓冲池）/ 1080p
 *     （16 缓冲池）下，分别保留 6 / 12 个空闲缓冲供解码器参考帧与轮转，
 *     确保不会耗尽 buffer group 导致解码卡顿。
 *   - **前置条件**：该流必须存在活跃的 AI 解码会话（即已有 AI 消费者挂载）。
 *     无解码会话时返回 Zhulong_ERR_NOT_FOUND，而不是自行拉起拉流。
 *   - **同步阻塞**：本函数执行 JPEG 编码，耗时与分辨率、裁剪框数量正相关
 *     （RK3568 上 1080p 单帧软编约 35~60ms）。严禁在数据包/AI 结果回调内调用，
 *     那样会触发 Zhulong_ERR_CALLBACK_CONTEXT。
 *   - **DMA-BUF 一致性**：若帧来自硬件解码（dma_fd >= 0），本函数会自行做
 *     CPU 侧 cache 同步；调用方无需也不得额外做 DMA_BUF_IOCTL_SYNC。
 *
 * @param engine   引擎句柄
 * @param stream   物理流 ID
 * @param request  抓拍参数；传 NULL 等价于「默认质量、仅全幅图」
 * @param[out] out_result 成功时接收结果句柄，**必须**由 Zhulong_snapshot_destroy 释放
 * @return Zhulong_status_t 状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法（空指针、box_count 越界、非有限坐标）；
 *         Zhulong_ERR_NOT_FOUND 流不存在或无活跃 AI 解码会话；
 *         Zhulong_ERR_NOT_RUNNING 引擎未启动；
 *         Zhulong_ERR_UNSUPPORTED 帧格式不支持（仅 NV12 / YUV420P）；
 *         Zhulong_ERR_OUT_OF_MEMORY 内存分配失败；
 *         Zhulong_ERR_CALLBACK_CONTEXT 在回调上下文中调用；
 *         Zhulong_ERR_INTERNAL 编码器内部错误
 */
Zhulong_status_t Zhulong_stream_snapshot(Zhulong_engine_h engine, Zhulong_stream_id stream,
    const Zhulong_snapshot_request *request, Zhulong_snapshot_h *out_result);

/**
 * @brief 查询抓拍结果中的图片数量
 *
 * 成功抓拍恒有 >= 1 张（全幅图）。空句柄返回 0。
 */
int32_t Zhulong_snapshot_count(Zhulong_snapshot_h result);

/**
 * @brief 按索引取出抓拍图
 *
 * @param result 结果句柄
 * @param index  索引，范围 [0, Zhulong_snapshot_count(result))
 * @return 指向内部图片描述的借用指针；索引越界或句柄为空时返回 NULL。
 *         该指针在 Zhulong_snapshot_destroy 之后失效。
 */
const Zhulong_snapshot_image *Zhulong_snapshot_at(Zhulong_snapshot_h result, int32_t index);

/**
 * @brief 释放抓拍结果及其全部图片数据
 *
 * ⚠️ 内存所有权：本函数释放 result 内部所有 image.data 与句柄本身。
 * 传入 NULL 是安全操作。重复释放同一句柄是未定义行为。
 */
void Zhulong_snapshot_destroy(Zhulong_snapshot_h result);

/**
 * @brief 订阅物理流的 AI 推理结果事件派发
 *
 * 约定与安全要求：
 *   - 本回调由 Native 推理结果转发线程异步触发。
 *   - 回调操作必须是快速、有界且非阻塞的，严禁抛出 C++ 异常。
 *   - 严禁在回调内部调用引擎的任何控制接口（如 acquire/release/probe/stop/destroy）。
 *   - token 为调用方注册的不透明整数，在 Go 中配合 cgo.Handle 使用，严禁直接传递裸 Go 指针。
 *
 * @param engine 引擎句柄
 * @param stream 流 ID
 * @param callback 结果到达时的回调函数指针
 * @param token 传递给回调函数的上下文令牌
 * @param[out] out_subscription 输出订阅 ID
 * @return Zhulong_status_t 操作状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法（空指针、非法流 ID）；
 *         Zhulong_ERR_NOT_FOUND 流不存在；
 *         Zhulong_ERR_NOT_RUNNING 引擎未启动
 */
Zhulong_status_t Zhulong_stream_subscribe_ai_results(Zhulong_engine_h engine,
    Zhulong_stream_id stream, Zhulong_ai_result_callback callback, uintptr_t token,
    Zhulong_subscription_id *out_subscription);

/**
 * @brief 取消 AI 推理结果事件订阅（同步排空语义）
 *
 * 同步排空保证：
 *   - 本函数成功返回后，保证所有正在执行此 token 的回调已彻底返回，且未来绝不会再触发。
 *   - Go 侧在调用本接口成功后，即可安全回收或解除关联的 cgo.Handle。
 *
 * @param engine 引擎句柄
 * @param stream 流 ID
 * @param subscription 待取消的订阅 ID
 * @return Zhulong_status_t 操作状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法；
 *         Zhulong_ERR_NOT_FOUND 流或订阅不存在；
 *         Zhulong_ERR_NOT_RUNNING 引擎未启动
 */
Zhulong_status_t Zhulong_stream_unsubscribe_ai_results(Zhulong_engine_h engine,
    Zhulong_stream_id stream, Zhulong_subscription_id subscription);

/**
 * @brief 向指定流或所有活跃流的 AI FrameBroker 广播下行控制报文（如底库热更新）
 *
 * @param engine 引擎句柄
 * @param stream 目标流 ID（传 0 表示广播至所有包含活跃 FrameBroker 的物理流）
 * @param msg_type 消息类型枚举（如 MSG_GALLERY_SYNC=10, MSG_GALLERY_UPSERT=11, MSG_GALLERY_DELETE=12）
 * @param payload 报文负载指针（可为 NULL）
 * @param payload_size 报文负载大小（不得超过 64KB）
 * @return Zhulong_status_t 状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法（如 payload_size > 64KB）；
 *         Zhulong_ERR_NOT_FOUND 指定流不存在或无运行中的 Broker；
 *         Zhulong_ERR_NOT_RUNNING 引擎未启动
 */
Zhulong_status_t Zhulong_engine_broadcast_ai_message(
    Zhulong_engine_h engine,
    Zhulong_stream_id stream,
    uint16_t msg_type,
    const void *payload,
    size_t payload_size
);

/* ========================================================================== */
/*                             流元数据探测接口                                */
/* ========================================================================== */

/**
 * @brief 独立同步探测 RTSP 流的视频元数据（分辨率、编码、帧率、参数集）
 *
 * 行为特点：
 *   - 建立单次独立的测试连接（不纳入流池池化管理）。
 *   - 可由 Zhulong_engine_stop 随时安全取消。
 *   - 内部使用单调递增时钟判断操作截止时间；操作失败时输出句柄置空。
 *
 * @param engine 引擎句柄
 * @param url RTSP 地址
 * @param options 连接配置（传 NULL 使用默认值）
 * @param[out] out_result 输出探测结果句柄指针（需调用 Zhulong_probe_result_destroy 释放）
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_engine_probe(Zhulong_engine_h engine, const char *url,
    const Zhulong_stream_options *options, Zhulong_probe_result_h *out_result);

/**
 * @brief 查看探测到的视频元数据视图
 *
 * 借用指针说明：
 *   - 输出结构中的指针直接指向探测结果内部存储，其生命周期与 result 句柄绑定。
 *   - 禁止修改或单独释放该内存。
 *
 * @param result 探测结果句柄
 * @param[out] out_view 输出元数据视图结构体指针
 * @return Zhulong_status_t 操作状态码
 */
Zhulong_status_t Zhulong_probe_result_view(Zhulong_probe_result_h result, Zhulong_video_view *out_view);

/**
 * @brief 销毁探测结果并释放相关内存
 *
 * @param result 待销毁的探测结果句柄（传入 NULL 是安全的）
 */
void Zhulong_probe_result_destroy(Zhulong_probe_result_h result);

/* ========================================================================== */
/*                             人脸特征提取接口                                */
/* ========================================================================== */


#ifdef __cplusplus
}
#endif
#endif /* ZHULONG_ENGINE_H */

