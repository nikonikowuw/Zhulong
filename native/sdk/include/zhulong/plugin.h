/**
 * @file plugin.h
 * @brief Zhulong 算法插件纯 C ABI 物理隔离规范 (V1)
 *
 * 本头文件定义了独立算法插件动态库（.so）必须遵循的纯 C ABI 契约。
 * 
 * 核心设计原则与边界铁律：
 * 1. 纯 C ABI 物理隔离：动态库边界严禁暴露 C++ 类、虚函数表、STL 容器或异常；
 * 2. 异常防爆门：所有导出函数必须以 try-catch 全面拦截异常并返回状态码，杜绝异常穿越 ABI；
 * 3. 内存所有权明确：推理结果内存由插件分配，且必须且仅能通过同一插件的 result_release 释放；
 * 4. 单块连续 Arena 打包：结构化结果（如人体嵌套人脸与候选人）在单次 malloc 内存块中连续排布，
 *    internal_token 记录该内存块首地址，释放时通过单次 std::free 彻底释放，杜绝嵌套泄漏；
 * 5. 严格坐标归一化：所有目标检测框、人体框、人脸框与关键点均采用 [x1, y1, x2, y2] ∈ [0.0, 1.0] 归一化坐标。
 */

#ifndef ZHULONG_PLUGIN_H
#define ZHULONG_PLUGIN_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief 插件状态码类型，0 表示成功，负数表示错误
 */
typedef int32_t zhulong_status_t;

/**
 * @brief 插件错误码枚举
 */
enum {
    ZHULONG_PLUGIN_OK                   = 0,   /**< 操作成功 */
    ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT = -1,  /**< 非法参数（如空指针、尺寸越界、未知配置） */
    ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY    = -2,  /**< 内存或显存分配失败 */
    ZHULONG_PLUGIN_ERR_NOT_SUPPORTED    = -3,  /**< 平台或算子不支持（如未适配的图像格式或模型特性） */
    ZHULONG_PLUGIN_ERR_NOT_FOUND        = -4,  /**< 资源未找到（如模型权重文件不存在） */
    ZHULONG_PLUGIN_ERR_ALREADY_EXISTS   = -5,  /**< 资源已存在或重复初始化 */
    ZHULONG_PLUGIN_ERR_INVALID_STATE    = -6,  /**< 句柄状态无效或生命周期未对齐 */
    ZHULONG_PLUGIN_ERR_TIMEOUT          = -7,  /**< 操作或推理执行超时 */
    ZHULONG_PLUGIN_ERR_INTERNAL         = -99  /**< 插件内部未捕获或未知错误 */
};

/**
 * @brief 图像像素色彩格式枚举
 */
typedef int32_t zhulong_pixel_format_t;

enum {
    ZHULONG_PIXEL_FORMAT_UNKNOWN = 0, /**< 未知格式 */
    ZHULONG_PIXEL_FORMAT_BGR24   = 1, /**< 打包 BGR 格式（每个像素 3 字节，B-G-R 顺序） */
    ZHULONG_PIXEL_FORMAT_RGB24   = 2, /**< 打包 RGB 格式（每个像素 3 字节，R-G-B 顺序） */
    ZHULONG_PIXEL_FORMAT_NV12    = 3, /**< 两平面 YUV420SP 格式（平面 0 为 Y，平面 1 为 UV 交错） */
    ZHULONG_PIXEL_FORMAT_YUV420P = 4, /**< 三平面 YUV420P 格式（I420，Y、U、V 三平面连续独立） */
    ZHULONG_PIXEL_FORMAT_RGBA32  = 5  /**< 打包 RGBA 格式（每个像素 4 字节，R-G-B-A 顺序） */
};

/* 长度与维度约束常量 */
#define ZHULONG_PERSON_ID_MAX_LEN    64 /**< 底库人员唯一标识符最大字节长度（含末尾 '\\0'） */
#define ZHULONG_PERSON_NAME_MAX_LEN  64 /**< 底库人员显示姓名最大字节长度（含末尾 '\\0'） */
#define ZHULONG_FACE_ID_MAX_LEN      64 /**< 底库单张人脸照片唯一标识符最大字节长度（含末尾 '\\0'） */
#define ZHULONG_FACE_LANDMARK_COUNT  5  /**< 人脸关键点标准数量（5 点：左眼、右眼、鼻尖、左嘴角、右嘴角） */

/**
 * @brief 推理结果载荷类型枚举
 */
typedef int32_t zhulong_result_type_t;

enum {
    ZHULONG_RESULT_TYPE_UNKNOWN          = 0,  /**< 未知结果类型 */
    ZHULONG_RESULT_TYPE_DETECTION        = 1,  /**< 通用目标检测结果（载荷为 zhulong_bbox[]） */
    ZHULONG_RESULT_TYPE_CLASSIFICATION   = 2,  /**< 图像分类结果（载荷为 zhulong_classification_item[]） */
    ZHULONG_RESULT_TYPE_PERSON_STRUCTURE = 3,  /**< 结构化人体/人脸结果（载荷为 zhulong_person_item[]） */
    ZHULONG_RESULT_TYPE_CUSTOM           = 99  /**< 自定义私有载荷类型 */
};

/**
 * @brief 跨进程/跨库只读图像帧视图
 *
 * 借用指针说明：
 *   - planes 内部的像素指针均为借用指针，其生命周期仅在 process 调用期间严格有效；
 *   - 插件若需在异步线程中持久持有图像数据，必须在 process 返回前完成私有内存深拷贝；
 *   - 多平面约定：单平面/打包格式使用 planes[0] 与 strides[0]；多平面格式依次填充 planes[0..N-1]；
 *   - 当 strides[0] == 0 时表示紧凑排列（无行间 padding，行跨度等于 width * 像素字节数）。
 */
typedef struct zhulong_frame_view {
    uint64_t frame_id;             /**< 帧序号 */
    uint64_t pts;                  /**< 显示时间戳（源流时间基下的原生滴答数） */
    int32_t width;                 /**< 图像有效像素宽度 */
    int32_t height;                /**< 图像有效像素高度 */
    zhulong_pixel_format_t format; /**< 图像像素色彩格式 */
    int32_t dma_buf_fd;            /**< 底层硬件 DMA-BUF 文件描述符（>=0 表示有效，<0 表示虚拟内存） */
    const uint8_t* planes[4];      /**< 各平面图像数据首地址借用指针数组 */
    int32_t strides[4];            /**< 各平面水平行跨度（字节数，含硬件对齐 padding）；strides[0]==0 表示紧凑排列 */
    int64_t timestamp_us;          /**< 采集单调时钟时间戳（微秒） */
} zhulong_frame_view;

/**
 * @brief 目标检测边界框
 *
 * 坐标系约定：
 *   - box 必须为严格归一化坐标 [x1, y1, x2, y2] ∈ [0.0, 1.0]；
 *   - x1, y1 为左上角坐标；x2, y2 为右下角坐标；
 *   - 结构体总尺寸固定为 88 字节，自然满足 8 字节对齐。
 */
typedef struct zhulong_bbox {
    float box[4];     /* [x1, y1, x2, y2] 严格归一化坐标 [0.0, 1.0] */
    float confidence; /* 置信度 (0.0 ~ 1.0) */
    int32_t class_id; /* 类别 ID */
    char label[64];   /* 类别名称 */
} zhulong_bbox;

/**
 * @brief 图像分类属性项
 */
typedef struct zhulong_classification_item {
    int32_t class_id;  /**< 类别 ID */
    float confidence;  /**< 分类置信度 (0.0 ~ 1.0) */
    char label[64];    /**< 类别名称（以 '\\0' 结尾） */
} zhulong_classification_item;

/**
 * @brief 人脸识别 Top-K 候选人
 *
 * 结构体总尺寸为 200 字节，通过 reserved_ 显式尾随对齐，满足 8 字节整除。
 */
typedef struct zhulong_face_candidate {
    char  person_id[ZHULONG_PERSON_ID_MAX_LEN];     /* 底库人员唯一标识符 */
    char  person_name[ZHULONG_PERSON_NAME_MAX_LEN]; /* 人员姓名 */
    char  face_id[ZHULONG_FACE_ID_MAX_LEN];         /* 底库单张人脸照片唯一标识符 (方案 B) */
    float similarity;                               /* 置信度分值 (0.0 ~ 1.0) */
    uint32_t reserved_;                             /* 显式填充对齐 (8 字节整除) */
} zhulong_face_candidate;

/**
 * @brief 单张人脸项
 *
 * 内存与指针约定：
 *   - landmarks 为 5 点归一化关键点坐标 [x, y] ∈ [0.0, 1.0]；
 *   - candidates 指针指向同一 Arena 内存块内的候选人数组，禁止对其单独执行 free；
 *   - 结构体总尺寸为 280 字节，满足 8 字节整除。
 */
typedef struct zhulong_face_item {
    float  box[4];                                   /* [x1, y1, x2, y2] 严格归一化坐标 [0.0, 1.0] */
    float  quality;                                  /* 人脸质量分 (0.0 ~ 1.0) */
    float  landmarks[ZHULONG_FACE_LANDMARK_COUNT][2];/* 5点关键点 (x, y)，归一化 [0.0, 1.0] */
    int32_t matched;                                 /* 1=命中底库，0=未命中 */
    float  similarity;                               /* Top-1 匹配置信度 (0.0 ~ 1.0) */
    int32_t is_fused;                                /* 是否多帧融合结果 (预留) */
    char   person_id[ZHULONG_PERSON_ID_MAX_LEN];      /* Top-1 人员 ID (未命中时为空字符串) */
    char   person_name[ZHULONG_PERSON_NAME_MAX_LEN];  /* Top-1 人员姓名 (未命中时为空字符串) */
    char   matched_face_id[ZHULONG_FACE_ID_MAX_LEN];  /* Top-1 命中的底照 ID (未命中时为空字符串) */
    uint32_t candidate_count;                        /* candidates 数组元素数 */
    uint32_t reserved_;                              /* 64位指针前置填充对齐 (8 字节对齐) */
    const zhulong_face_candidate* candidates;        /* 指向候选人数组 (只读，指向同一 Arena) */
} zhulong_face_item;

/**
 * @brief 结构化人体项 (1 个人体挂载 0~N 张人脸)
 *
 * 纯人脸场景退化语义约定：
 *   - 纯人脸检测场景下，每个检出的人脸映射为一个 zhulong_person_item；
 *   - track_id 填 0；
 *   - body_box 填 face_box 的归一化坐标；
 *   - body_score 填 1.0f；
 *   - face_count 填 1，faces 指向对应的人脸项。
 * 结构体总尺寸为 40 字节，满足 8 字节整除。
 */
typedef struct zhulong_person_item {
    int64_t  track_id;                               /* 跟踪 ID (纯人脸场景填 0) */
    float    body_box[4];                            /* [x1, y1, x2, y2] 严格归一化 (纯人脸场景同 face_box) */
    float    body_score;                             /* 人体检出置信度 (纯人脸场景填 1.0) */
    uint32_t face_count;                             /* faces 数组元素数 */
    const zhulong_face_item* faces;                  /* 指向人脸数组 (只读，指向同一 Arena) */
} zhulong_person_item;

/**
 * @brief 通用结构化感知推理结果
 *
 * 内存所有权核心约定：
 *   - 本结构体及其载荷由插件内部分配；
 *   - 外部调用者获取结果后只读使用，严禁单独 free data 中的子指针；
 *   - 使用完毕后，必须且只能将该结构体指针传回同一插件的 result_release 释放；
 *   - internal_token 为插件私有销毁凭据（在 Arena 模式下指向整体连续内存首地址）。
 */
typedef struct zhulong_inference_result {
    uint64_t frame_id;                 /**< 对应的视频帧序号 */
    uint64_t task_id;                  /**< 对应的摄像机任务 ID */
    zhulong_result_type_t result_type; /**< 结果载荷类型标识 */
    uint32_t count;                    /**< 顶层结果数组元素数量（如检出的检测框数或人体数） */
    void* data;                        /**< 指向结果元素数组的指针 (zhulong_bbox[] / zhulong_classification_item[] / zhulong_person_item[]) */
    void* internal_token;              /**< 插件内部私有销毁凭据（释放时交由 result_release 处理） */
} zhulong_inference_result;

/** @brief 模型资源组不透明句柄定义（整包共享的不可变模型权重资源） */
typedef struct zhulong_model_group_t* zhulong_model_group_h;

/** @brief Worker 执行上下文不透明句柄定义（线程独占的执行会话与工作区） */
typedef struct zhulong_worker_ctx_t*  zhulong_worker_ctx_h;

/** @brief 任务状态不透明句柄定义（单路摄像机时序状态，如跟踪轨迹） */
typedef struct zhulong_task_ctx_t*    zhulong_task_ctx_h;

/**
 * @brief 版本化插件接口函数表 (V1)
 *
 * 插件动态库导出主符号 zhulong_plugin_get_api 后填充本表。
 */
typedef struct zhulong_plugin_api_v1 {
    uint32_t struct_size;         /**< 接口表结构体大小 (sizeof(zhulong_plugin_api_v1))，用于向前兼容性校验 */
    uint32_t api_version;         /**< API 版本号，必须恒等于 1 */
    const char* plugin_name;      /**< 插件人类可读名称字符串 */
    const char* plugin_version;   /**< 插件语义化版本号字符串 (如 "1.0.0") */

    /**
     * @brief 1. 模型资源组生命周期：创建不可变模型资源组 (整个插件包共享不可变模型资源)
     *
     * @param config_json 插件配置 JSON 字符串 (如权重路径、阈值等)
     * @param[out] out_model 输出创建成功的模型资源组不透明句柄
     * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；其他为错误码
     */
    zhulong_status_t (*model_create)(const char* config_json, zhulong_model_group_h* out_model);

    /**
     * @brief 销毁模型资源组并释放全局权重资源
     *
     * @param model 待销毁的模型资源组句柄
     */
    void             (*model_destroy)(zhulong_model_group_h model);

    /**
     * @brief 2. Worker 执行上下文生命周期：创建线程独占的 Worker 上下文
     *
     * @param model 模型资源组句柄
     * @param worker_id 执行线程/Worker 唯一编号
     * @param[out] out_worker 输出创建成功的 Worker 执行上下文句柄
     * @return zhulong_status_t 操作状态码
     */
    zhulong_status_t (*worker_create)(zhulong_model_group_h model, uint32_t worker_id, zhulong_worker_ctx_h* out_worker);

    /**
     * @brief 销毁 Worker 执行上下文并释放独占的工作区内存
     *
     * @param worker 待销毁的 Worker 执行上下文句柄
     */
    void             (*worker_destroy)(zhulong_worker_ctx_h worker);

    /**
     * @brief 3. 任务状态绑定：为特定摄像机打开时序任务状态 (如初始化目标跟踪器)
     *
     * @param worker Worker 执行上下文句柄
     * @param task_id 摄像机/流任务唯一编号
     * @param[out] out_task 输出创建成功的任务状态句柄
     * @return zhulong_status_t 操作状态码
     */
    zhulong_status_t (*task_open)(zhulong_worker_ctx_h worker, uint64_t task_id, zhulong_task_ctx_h* out_task);

    /**
     * @brief 关闭特定摄像机的任务状态并释放时序跟踪状态
     *
     * @param task 待关闭的任务状态句柄
     */
    void             (*task_close)(zhulong_task_ctx_h task);

    /**
     * @brief 4. 同步推理执行：对输入图像帧执行推理并输出结构化感知结果
     *
     * ⚠️ 内存与异常契约：
     *   - 函数内部必须以 try-catch 拦截所有 C++ 异常，失败时返回错误码并将 *out_result 置为 nullptr；
     *   - 成功时分配 zhulong_inference_result 结构体，外部使用后必须调用 result_release 归还。
     *
     * @param task 任务状态句柄
     * @param frame 输入图像帧的只读借用视图
     * @param[out] out_result 输出分配的推理结果指针
     * @return zhulong_status_t 操作状态码
     */
    zhulong_status_t (*process)(zhulong_task_ctx_h task, const zhulong_frame_view* frame, zhulong_inference_result** out_result);

    /**
     * @brief 释放由 process 产出的推理结果及其关联的 Arena 内存
     *
     * @param result 待释放的推理结果指针 (传入 nullptr 是安全操作)
     */
    void             (*result_release)(zhulong_inference_result* result);
} zhulong_plugin_api_v1;

/**
 * @brief 插件动态库必须导出的唯一主符号名称宏定义
 */
#define ZHULONG_PLUGIN_ENTRY_POINT "zhulong_plugin_get_api"

#if defined(_WIN32)
#  if defined(ZHULONG_PLUGIN_BUILD)
#    define ZHULONG_PLUGIN_EXPORT __declspec(dllexport)
#  else
#    define ZHULONG_PLUGIN_EXPORT __declspec(dllimport)
#  endif
#elif defined(__GNUC__) || defined(__clang__)
#  define ZHULONG_PLUGIN_EXPORT __attribute__((visibility("default")))
#else
#  define ZHULONG_PLUGIN_EXPORT
#endif

/**
 * @brief 插件动态库必须导出的唯一主符号函数原型
 *
 * 宿主在 dlopen 加载插件后调用此函数获取对应版本的 API 函数表。
 *
 * @param requested_version 宿主请求的 API 版本号 (当前必须为 1)
 * @param[out] out_api_table 宿主分配的 API 函数表缓冲指针 (类型为 zhulong_plugin_api_v1*)
 * @param table_size 宿主提供的缓冲字节大小 (必须 >= sizeof(zhulong_plugin_api_v1))
 * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；ZHULONG_PLUGIN_ERR_NOT_SUPPORTED 版本不支持；ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT 缓冲为空或太小
 */
ZHULONG_PLUGIN_EXPORT zhulong_status_t zhulong_plugin_get_api(
    uint32_t requested_version,
    void* out_api_table,
    size_t table_size
);

#ifdef __cplusplus
}
#endif

#endif /* ZHULONG_PLUGIN_H */
