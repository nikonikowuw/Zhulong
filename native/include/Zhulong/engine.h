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
} Zhulong_snapshot_request;

/** @brief 抓拍结果的不透明句柄类型，由 Zhulong_stream_snapshot 分配 */
typedef struct Zhulong_snapshot_result_t *Zhulong_snapshot_h;

/**
 * @brief 对指定流的 AI 解码会话当前最新一帧执行抓拍（全幅 + 可选裁剪），输出 JPEG
 *
 * 语义与限制（⚠️ 请完整阅读后再使用）：
 *   - **帧来源**：取的是该流 AI 解码会话中**当前最新的一帧**，不是产生某个推理结果的那一帧。
 *     抓拍与推理结果之间存在「结果回传 + 宿主处理」的固有延迟，实测约 1~4 帧。
 *     调用方必须把该偏差视为已知语义，而不是缺陷。
 *   - **前置条件**：该流必须存在活跃的 AI 解码会话（即已有 AI 消费者挂载）。
 *     无解码会话时返回 Zhulong_ERR_NOT_FOUND，而不是自行拉起拉流。
 *   - **持帧上限**：Native 侧只为快照保留最后 1 帧引用。超过 1 帧会把解码器
 *     buffer group 耗尽（1080p 下仅 16 个 buffer）并导致画面卡顿。
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

/**
 * @brief 从图像像素数据同步提取 512 维人脸特征向量
 *
 * 内存与调用契约：
 *   - pixel_data: 连续像素数据借用指针，调用期间有效；
 *   - width / height: 图像宽高（像素）；
 *   - stride: 水平行扫描跨度（字节数，0 表示紧凑排列 width * 3）；
 *   - format: 像素色彩格式（当前支持 1=BGR24, 2=RGB24）；
 *   - out_features: 调用方预分配的 float 数组缓冲区，容量必须 >= 512；
 *   - feature_dim: 输入缓冲区容量（按 float 计数），输出实际写入特征维度（固定 512）；
 *   - 特征归一化保证：提取出的 512 维向量严格满足 L2 范数单位归一化（||v|| = 1.0）；
 *   - 线程安全：可在任意线程并发调用，不占用或阻塞流媒体流水线。
 *
 * 能力来源与闭环报错（Fail-Closed）：
 *   - 真实推理由动态加载的人脸识别算法插件（libFaceRecognitionPlugin.so）提供：首次调用懒加载，
 *     空闲超过 ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC（默认 60 秒）后自动释放模型并卸载动态库；
 *   - 插件缺失、符号缺失或模型不可用时**显式返回错误码**，绝不允许回退到模拟特征向量。
 *
 * @param engine 引擎句柄（可为 NULL 或当前实例句柄）
 * @param pixel_data 连续像素数据首地址
 * @param width 图像宽度
 * @param height 图像高度
 * @param stride 行跨度（字节数）
 * @param format 像素格式（如 RGB24 / BGR24）
 * @param[out] out_features 输出特征向量缓冲区
 * @param[in,out] feature_dim 缓冲区容量输入与实际维度输出
 * @return Zhulong_status_t 操作状态码：
 *         Zhulong_OK 成功；
 *         Zhulong_ERR_INVALID_ARGUMENT 参数非法（空指针、非法尺寸或缓冲区容量 < 512）；
 *         Zhulong_ERR_UNSUPPORTED 像素格式不支持，或人脸算法插件/模型不可用；
 *         Zhulong_ERR_NOT_FOUND 未在图像中检测到人脸；
 *         Zhulong_ERR_INTERNAL 插件内部推理异常
 */
Zhulong_status_t Zhulong_engine_extract_face_feature(
    Zhulong_engine_h engine,
    const uint8_t *pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float *out_features,
    uint32_t *feature_dim
);

#ifdef __cplusplus
}
#endif
#endif /* ZHULONG_ENGINE_H */

