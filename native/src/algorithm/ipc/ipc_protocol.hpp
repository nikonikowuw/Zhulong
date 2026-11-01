/**
 * @file ipc_protocol.hpp
 * @brief 宿主进程与算法 Runner 子进程间点对点二进制 IPC 控制协议
 *
 * 核心设计原则：
 * 1. 严格 8 字节自然对齐：IPC 二进制包中所有 64 位字段（generation、stream_id、frame_id、pts）
 *    必须排布在 8 字节自然对齐偏移上；结构体总尺寸必须为 8 的倍数并包含尾部对齐填充；
 *    通过 static_assert(offsetof(...) % 8 == 0) 与总大小编译期校验，彻底杜绝 ARM64/嵌入式体系下
 *    非对齐访问与 -Waddress-of-packed-member 警告。
 * 2. 64KB 最大 Payload 门禁：MAX_IPC_PAYLOAD_SIZE 与 Go 宿主侧 internal/algorithm/process.go 的
 *    MaxIpcPayloadSize 必须保持一致（由 internal/algorithm/ipc_contract_test.go 执行跨语言比对校验），
 *    防止超大异常报文击穿接收缓冲。
 * 3. 槽位代次保护（generation）：通过单调递增的代次字段实现防 ABA 悬挂确认，过期租约 ACK 静默丢弃。
 */

#pragma once

#include <cstdint>
#include <cstddef>
#include <zhulong/plugin.h>

namespace zhulong::ipc {

constexpr uint32_t IPC_MAGIC = 0x5A484C47; /**< IPC 协议魔数：ASCII 'ZHLG' (0x5A484C47) */
constexpr uint16_t IPC_VERSION = 1;        /**< IPC 协议版本号 */

/**
 * @brief 最大单条 IPC 消息负载字节上限（64KB）
 *
 * ⚠️ 跨语言硬约束：Go 宿主侧 internal/algorithm/process.go 的 MaxIpcPayloadSize 常量
 * 必须与本数值严格相等，由 internal/algorithm/ipc_contract_test.go 进行跨语言比对，
 * 超过该上限的报文在两端均会被强制截断并熔断连接。
 */
constexpr uint32_t MAX_IPC_PAYLOAD_SIZE = 64 * 1024;

/**
 * @brief IPC 消息类型枚举
 */
enum MessageType : uint16_t {
    MSG_UNKNOWN         = 0, /**< 未知消息类型（非法值） */
    MSG_HANDSHAKE_REQ   = 1, /**< 握手请求包（Client -> Broker）：上报 client_name 与客户端期望参数 */
    MSG_HANDSHAKE_RESP  = 2, /**< 握手响应包（Broker -> Client）：回传分配的 client_id、共享内存名称与槽位拓扑 */
    MSG_FRAME_DISPATCH  = 3, /**< 帧分发通知包（Broker -> Client）：下发槽位索引、代次、分辨率与各平面步长 */
    MSG_FRAME_ACK       = 4, /**< 帧处理确认包（Client -> Broker）：归还槽位租约，附带 generation 防 ABA */
    MSG_HEARTBEAT       = 5, /**< 双向心跳包：用于检测失联或卡死进程 */
    MSG_DISCONNECT      = 6, /**< 优雅断开通知包：通知对端即将关闭通信通道 */
    MSG_RESULT          = 7, /**< 推理结果载荷包（Client -> Broker）：回传由 Runner 序列化的推理输出 */
    MSG_GALLERY_SYNC    = 10, /**< 底库全量同步通知包（Broker -> Client）：下发人员特征底库分片 */
    MSG_GALLERY_UPSERT  = 11, /**< 底库增量注册包（Broker -> Client）：下发单张新增/更新人脸特征 */
    MSG_GALLERY_DELETE  = 12, /**< 底库增量删除包（Broker -> Client）：通知删除指定人脸或人员特征 */
    MSG_GALLERY_ACK     = 13, /**< 底库同步确认包（Client -> Broker）：全量底库分片聚合完成确认 */
    MSG_TASK_CONFIG     = 14  /**< 任务级配置下发包（Broker -> Client）：每路流一份，见 TaskConfigMsg */
};

#pragma pack(push, 1)

/**
 * @brief 定长 16 字节通用 IPC 报文头部
 *
 * 所有通过 Unix 域套接字发送的消息均以此头部作为前导，接收端先读入 16 字节头部，
 * 校验 magic 与 payload_size 合法性后，再读入对应长度的负载。
 */
struct IpcHeader {
    uint32_t magic{IPC_MAGIC};       /**< 协议魔数，必须恒等于 IPC_MAGIC */
    uint16_t msg_type{MSG_UNKNOWN};  /**< 消息类型，对应 MessageType 枚举值 */
    uint16_t version{IPC_VERSION};   /**< 协议版本号，当前恒为 1 */
    uint32_t payload_size{0};        /**< 后续负载字节数（<= MAX_IPC_PAYLOAD_SIZE） */
    uint32_t sequence{0};            /**< 报文单调自增序号 */
};

/**
 * @brief 握手请求负载（MSG_HANDSHAKE_REQ，定长 40 字节）
 */
struct HandshakeReq {
    uint32_t client_id{0};       /**< 期望客户端 ID（0 表示由 Broker 动态分配） */
    uint32_t api_version{1};     /**< 客户端遵循的 API 版本号 */
    char     client_name[32]{0}; /**< 客户端可读标识名称（UTF-8 字符串，以 \0 结尾） */
};

/**
 * @brief 握手响应负载（MSG_HANDSHAKE_RESP，定长 80 字节）
 */
struct HandshakeResp {
    uint32_t client_id{0};       /**< Broker 分配给该客户端的全局唯一 ID */
    uint32_t status{0};          /**< 握手状态码（0 表示成功） */
    char     shm_name[64]{0};    /**< 对应物理流的共享内存段名称（如 /zhulong_shm_1） */
    uint32_t slot_count{0};      /**< 共享内存中的槽位总数 */
    uint32_t slot_size{0};       /**< 单个槽位字节数 */
};

constexpr size_t MAX_IPC_MOTION_BOXES = 4;

/**
 * @brief 帧分发标志位枚举
 */
constexpr uint32_t FRAME_DISPATCH_FLAG_DMA_FD = (1U << 0); /**< 载荷由 SCM_RIGHTS 传输的 DMA-BUF 文件描述符承载，不得回退读取 SHM 槽位 */

/**
 * @brief 归一化运动外接矩形 [x1, y1, x2, y2] ∈ [0.0, 1.0]（定长 16 字节）
 */
struct IpcMotionBox {
    float x1{0.0f};
    float y1{0.0f};
    float x2{0.0f};
    float y2{0.0f};
};

/**
 * @brief 帧运动检测元信息（用于 FrameBroker 构造分发报文）
 */
struct FrameMotionInfo {
    uint32_t has_motion{1};       /**< 1 表示存在运动或未做检测（默认放行）；0 表示完全静止 */
    uint32_t motion_box_count{0}; /**< 有效外接框数量 */
    IpcMotionBox motion_boxes[MAX_IPC_MOTION_BOXES]{}; /**< 归一化外接矩形 */
};

/**
 * @brief 帧分发通知负载（MSG_FRAME_DISPATCH，定长 168 字节）
 *
 * ⚠️ 64 位整数字段前置并严格保持 8 字节自然对齐，尾部含运动检测扩展字段保证结构体总尺寸被 8 整除。
 *
 * ⚠️ **前缀兼容铁律**：运动检测字段追加在既有 96 字节布局的**尾部**，前 96 字节一个字节都不许动。
 *    旧宿主（96 字节报文）配新 Runner 时，后者必须按遗留格式解析并退化为「全帧推理」；
 *    因为宿主与算法包分开交付（`make sdk-dist`），两个方向的版本偏斜都会真实发生。
 */
struct FrameDispatchMsg {
    uint64_t stream_id{0};       /**< 物理流全局唯一 ID */
    uint64_t frame_id{0};        /**< 视频帧全局单调自增序号 */
    uint64_t generation{0};      /**< 槽位写入代次：前置确保 8 字节自然对齐，归还租约时必须原样带回 */
    int64_t  pts{0};             /**< 帧呈现时间戳（遵循源流时间基） */
    int64_t  capture_time_ms{0}; /**< 帧物理采集/抓拍时刻的绝对 UTC 毫秒时间戳 */
    uint32_t slot_index{0};      /**< 帧数据所在的共享内存槽位索引（0 ~ slot_count-1） */
    int32_t  width{0};           /**< 画面像素宽度 */
    int32_t  height{0};          /**< 画面像素高度 */
    int32_t  format{ZHULONG_PIXEL_FORMAT_UNKNOWN}; /**< 像素格式（如 NV12、YUV420P、BGR24） */
    int32_t  strides[4]{0};      /**< 各平面的行扫描步长（字节） */
    uint32_t offsets[4]{0};      /**< 各平面相对于对应槽位起始物理地址的偏移量（字节） */
    int32_t  reserved_fd{-1};    /**< 对齐占位字段，恒为 -1。
                              *   ⚠️ 严禁写入宿主进程的 dma_fd：fd 号是进程本地句柄，
                              *   跨进程传值会让接收端对无关资源执行 mmap/ioctl。
                              *   真实句柄只经 SCM_RIGHTS 传递。 */
    uint32_t flags{0};           /**< 帧属性标志位（低位扩展，保持 8 字节对齐；如 FRAME_DISPATCH_FLAG_DMA_FD） */

    /* ---------------- 运动检测扩展字段 (定长 72 字节) ---------------- */
    uint32_t has_motion{1};       /**< 1 表示有运动或未启用检测（默认放行）；0 表示画面静止 */
    uint32_t motion_box_count{0}; /**< 有效运动外接框数量（0 ~ MAX_IPC_MOTION_BOXES） */
    IpcMotionBox motion_boxes[MAX_IPC_MOTION_BOXES]{}; /**< 4 * 16 = 64 字节归一化坐标 [x1, y1, x2, y2] */
};

/**
 * @brief 运动检测字段落地前的分发报文尺寸（96 字节），新 Runner 的前缀兼容下限
 *
 * 由 `static_assert` 约束为「运动字段之前的所有内容」，杜绝有人往中间插字段。
 */
constexpr size_t LEGACY_FRAME_DISPATCH_SIZE = 96;

/**
 * @brief 帧处理确认负载（MSG_FRAME_ACK，定长 32 字节）
 *
 * 算法 Runner 处理完某帧后回传此包。Broker 校验 generation 若匹配则扣减在途计数并释放租约；
 * 若 generation 不匹配（说明该槽位已被驱逐复用），则静默丢弃此过期 ACK。
 */
struct FrameAckMsg {
    uint64_t stream_id{0};       /**< 物理流全局唯一 ID */
    uint64_t frame_id{0};        /**< 确认完成的视频帧全局序号 */
    uint64_t generation{0};      /**< 槽位代次（用于防 ABA 校验） */
    uint32_t slot_index{0};      /**< 归还的共享内存槽位索引 */
    uint32_t client_id{0};       /**< 客户端自身唯一 ID */
};

/**
 * @brief 进程间心跳测试负载（MSG_HEARTBEAT，定长 8 字节）
 */
struct HeartbeatMsg {
    uint64_t timestamp_ms{0};    /**< 发送端单调毫秒时间戳 */
};

/**
 * @brief 任务级配置下发负载头（MSG_TASK_CONFIG，变长 68 字节头 + 尾随 JSON）
 *
 * 负载 = 本结构后紧跟 config_json_len 字节 UTF-8 JSON（宿主侧已按 config.schema.json
 * 校验过，骨架**不解析** JSON：原样交给算法侧，由算法自己的配置解析器消费）。
 *
 * ⚠️ algorithm_id 是唯一防线：FrameBroker 的广播会同时到达同一路流上的所有算法包
 *    （人脸 + 车牌…），客户端必须与自己的 `--algorithm-id` 比对，不匹配即丢弃。
 *    64 字节与 manifest.id 的 manifestTokenPattern 上限一致。
 */
struct TaskConfigMsg {
    uint32_t config_json_len{0};   /**< 紧随其后的 JSON 字节数（头 + JSON ≤ MAX_IPC_PAYLOAD_SIZE） */
    char     algorithm_id[64]{0};  /**< 目标算法包 ID；与骨架 --algorithm-id 不等则丢弃 */
};

#pragma pack(pop)

static_assert(sizeof(IpcHeader) == 16, "IpcHeader must be 16 bytes");
static_assert(sizeof(HandshakeReq) == 40, "HandshakeReq size mismatch");
static_assert(sizeof(HandshakeResp) == 80, "HandshakeResp size mismatch");
static_assert(sizeof(IpcMotionBox) == 16, "IpcMotionBox size mismatch");
static_assert(sizeof(FrameDispatchMsg) == 168, "FrameDispatchMsg size mismatch");
static_assert(sizeof(FrameAckMsg) == 32, "FrameAckMsg size mismatch");
static_assert(sizeof(TaskConfigMsg) == 68, "TaskConfigMsg size mismatch");

static_assert(offsetof(FrameDispatchMsg, stream_id) % 8 == 0, "stream_id unaligned");
static_assert(offsetof(FrameDispatchMsg, frame_id) % 8 == 0, "frame_id unaligned");
static_assert(offsetof(FrameDispatchMsg, generation) % 8 == 0, "generation unaligned");
static_assert(offsetof(FrameDispatchMsg, pts) % 8 == 0, "pts unaligned");
static_assert(offsetof(FrameDispatchMsg, capture_time_ms) % 8 == 0, "capture_time_ms unaligned");
static_assert(offsetof(FrameDispatchMsg, flags) % 4 == 0, "flags unaligned");
static_assert(offsetof(FrameDispatchMsg, has_motion) % 8 == 0, "has_motion unaligned");
static_assert(offsetof(FrameDispatchMsg, motion_boxes) % 8 == 0, "motion_boxes unaligned");
static_assert(LEGACY_FRAME_DISPATCH_SIZE == offsetof(FrameDispatchMsg, has_motion),
              "legacy dispatch prefix must end exactly where motion fields begin");

static_assert(offsetof(FrameAckMsg, stream_id) % 8 == 0, "stream_id unaligned");
static_assert(offsetof(FrameAckMsg, frame_id) % 8 == 0, "frame_id unaligned");
static_assert(offsetof(FrameAckMsg, generation) % 8 == 0, "generation unaligned");

} // namespace zhulong::ipc

