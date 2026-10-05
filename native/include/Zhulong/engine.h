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
 * @brief 视频编解码器枚举
 */
enum {
    Zhulong_CODEC_H264 = 1,               /**< H.264 / AVC */
    Zhulong_CODEC_H265 = 2                /**< H.265 / HEVC */
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
} Zhulong_packet_view;

/**
 * @brief 流状态信息结构体
 */
typedef struct {
    int32_t state;               /**< 流当前运行状态（Zhulong_STREAM_STARTING / RUNNING / FAILED） */
    Zhulong_status_t error;      /**< 安全的定长错误码，避免向上暴露未经脱敏的 FFmpeg 错误文本或 URL 凭据 */
} Zhulong_stream_status;

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

/* ========================================================================== */
/*                             引擎生命周期管理接口                              */
/* ========================================================================== */

/**
 * @brief 创建并分配 Native 引擎实例
 *
 * @param[out] out_engine 输出参数，接收创建成功后的引擎句柄指针
 * @return Zhulong_status_t Zhulong_OK 成功；Zhulong_ERR_INVALID_ARGUMENT 参数空；Zhulong_ERR_OUT_OF_MEMORY 内存不足
 */
Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine);

/**
 * @brief 启动 Native 引擎及内部清理/调度线程
 *
 * @param engine 引擎句柄
 * @return Zhulong_status_t Zhulong_OK 成功；具备幂等性（重复调用返回成功）
 */
Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine);

/**
 * @brief 停止 Native 引擎
 *
 * 取消所有活跃拉流和探测、回收 Worker 线程、排空回调，并使所有流 ID 与订阅 ID 失效。
 * 具备幂等性；停止后若再次调用 start，将以全新空的流池重新运行。
 *
 * @param engine 引擎句柄
 * @return Zhulong_status_t Zhulong_OK 成功
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
 * @return Zhulong_status_t 操作状态码
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
 * @brief 订阅物理流的实时数据包派发
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

#ifdef __cplusplus
}
#endif
#endif /* ZHULONG_ENGINE_H */
