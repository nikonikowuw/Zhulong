#ifndef ZHULONG_PLUGIN_H
#define ZHULONG_PLUGIN_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* 插件状态码 */
typedef int32_t zhulong_status_t;

enum {
    ZHULONG_PLUGIN_OK = 0,
    ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT = -1,
    ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY = -2,
    ZHULONG_PLUGIN_ERR_NOT_SUPPORTED = -3,
    ZHULONG_PLUGIN_ERR_NOT_FOUND = -4,
    ZHULONG_PLUGIN_ERR_ALREADY_EXISTS = -5,
    ZHULONG_PLUGIN_ERR_INVALID_STATE = -6,
    ZHULONG_PLUGIN_ERR_TIMEOUT = -7,
    ZHULONG_PLUGIN_ERR_INTERNAL = -99
};

/* 图像色彩格式 */
typedef int32_t zhulong_pixel_format_t;

enum {
    ZHULONG_PIXEL_FORMAT_UNKNOWN = 0,
    ZHULONG_PIXEL_FORMAT_BGR24   = 1,
    ZHULONG_PIXEL_FORMAT_RGB24   = 2,
    ZHULONG_PIXEL_FORMAT_NV12    = 3,
    ZHULONG_PIXEL_FORMAT_YUV420P = 4,
    ZHULONG_PIXEL_FORMAT_RGBA32  = 5
};

/* 长度与维度约束常量 */
#define ZHULONG_PERSON_ID_MAX_LEN    64
#define ZHULONG_PERSON_NAME_MAX_LEN  64
#define ZHULONG_FACE_LANDMARK_COUNT  5

/* 结果类型 */
typedef int32_t zhulong_result_type_t;

enum {
    ZHULONG_RESULT_TYPE_UNKNOWN          = 0,
    ZHULONG_RESULT_TYPE_DETECTION        = 1,
    ZHULONG_RESULT_TYPE_CLASSIFICATION   = 2,
    ZHULONG_RESULT_TYPE_PERSON_STRUCTURE = 3,
    ZHULONG_RESULT_TYPE_CUSTOM           = 99
};

/* 跨进程/跨库只读图像帧视图 */
typedef struct zhulong_frame_view {
    uint64_t frame_id;
    uint64_t pts;
    int32_t width;
    int32_t height;
    zhulong_pixel_format_t format;
    const uint8_t* planes[4];
    int32_t strides[4];
    int64_t timestamp_us;
} zhulong_frame_view;

/* 目标检测边界框 */
typedef struct zhulong_bbox {
    float x;          /* 归一化或像素坐标 x (左上) */
    float y;          /* 归一化或像素坐标 y (左上) */
    float width;      /* 宽度 */
    float height;     /* 高度 */
    float confidence; /* 置信度 (0.0 ~ 1.0) */
    int32_t class_id; /* 类别 ID */
    char label[64];   /* 类别名称 */
} zhulong_bbox;

/* 图像分类属性项 */
typedef struct zhulong_classification_item {
    int32_t class_id;
    float confidence;
    char label[64];
} zhulong_classification_item;

/* Top-K 候选人 */
typedef struct zhulong_face_candidate {
    char  person_id[ZHULONG_PERSON_ID_MAX_LEN];     /* 底库人员唯一标识符 */
    char  person_name[ZHULONG_PERSON_NAME_MAX_LEN]; /* 人员姓名 */
    float similarity;                               /* 置信度分值 (0.0 ~ 1.0) */
    uint32_t reserved_;                             /* 显式填充对齐 (8 字节整除) */
} zhulong_face_candidate;

/* 单张人脸项 */
typedef struct zhulong_face_item {
    float  box[4];                                   /* [x1, y1, x2, y2] 严格归一化坐标 [0.0, 1.0] */
    float  quality;                                  /* 人脸质量分 (0.0 ~ 1.0) */
    float  landmarks[ZHULONG_FACE_LANDMARK_COUNT][2];/* 5点关键点 (x, y)，归一化 [0.0, 1.0] */
    int32_t matched;                                 /* 1=命中底库，0=未命中 */
    float  similarity;                               /* Top-1 匹配置信度 (0.0 ~ 1.0) */
    int32_t is_fused;                                /* 是否多帧融合结果 (预留) */
    char   person_id[ZHULONG_PERSON_ID_MAX_LEN];      /* Top-1 人员 ID (未命中时为空字符串) */
    char   person_name[ZHULONG_PERSON_NAME_MAX_LEN];  /* Top-1 人员姓名 (未命中时为空字符串) */
    uint32_t candidate_count;                        /* candidates 数组元素数 */
    uint32_t reserved_;                              /* 64位指针前置填充对齐 (8 字节对齐) */
    const zhulong_face_candidate* candidates;        /* 指向候选人数组 (只读，指向同一 Arena) */
} zhulong_face_item;

/* 结构化人体项 (1 个人体挂载 0~N 张人脸) */
typedef struct zhulong_person_item {
    int64_t  track_id;                               /* 跟踪 ID (纯人脸场景填 0) */
    float    body_box[4];                            /* [x1, y1, x2, y2] 严格归一化 (纯人脸场景同 face_box) */
    float    body_score;                             /* 人体检出置信度 (纯人脸场景填 1.0) */
    uint32_t face_count;                             /* faces 数组元素数 */
    const zhulong_face_item* faces;                  /* 指向人脸数组 (只读，指向同一 Arena) */
} zhulong_person_item;

/* 通用结构化感知推理结果 */
typedef struct zhulong_inference_result {
    uint64_t frame_id;
    uint64_t task_id;
    zhulong_result_type_t result_type;
    uint32_t count;
    void* data;              /* 指向 zhulong_bbox[]、zhulong_classification_item[] 或 zhulong_person_item[] 等数组 */
    void* internal_token;    /* 插件内部私有销毁凭据 */
} zhulong_inference_result;

/* 不透明句柄定义 */
typedef struct zhulong_model_group_t* zhulong_model_group_h;
typedef struct zhulong_worker_ctx_t*  zhulong_worker_ctx_h;
typedef struct zhulong_task_ctx_t*    zhulong_task_ctx_h;

/* 版本化插件接口表 (V1) */
typedef struct zhulong_plugin_api_v1 {
    uint32_t struct_size;
    uint32_t api_version; /* 必须为 1 */
    const char* plugin_name;
    const char* plugin_version;

    /* 1. 模型资源组生命周期 (整个包共享不可变模型资源) */
    zhulong_status_t (*model_create)(const char* config_json, zhulong_model_group_h* out_model);
    void             (*model_destroy)(zhulong_model_group_h model);

    /* 2. Worker 执行上下文生命周期 (独占执行缓冲区与上下文) */
    zhulong_status_t (*worker_create)(zhulong_model_group_h model, uint32_t worker_id, zhulong_worker_ctx_h* out_worker);
    void             (*worker_destroy)(zhulong_worker_ctx_h worker);

    /* 3. 任务状态绑定 (单摄像机时序状态，如跟踪轨迹) */
    zhulong_status_t (*task_open)(zhulong_worker_ctx_h worker, uint64_t task_id, zhulong_task_ctx_h* out_task);
    void             (*task_close)(zhulong_task_ctx_h task);

    /* 4. 同步推理与结果释放 */
    zhulong_status_t (*process)(zhulong_task_ctx_h task, const zhulong_frame_view* frame, zhulong_inference_result** out_result);
    void             (*result_release)(zhulong_inference_result* result);
} zhulong_plugin_api_v1;

/* 插件动态库必须导出的唯一主符号 */
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

ZHULONG_PLUGIN_EXPORT zhulong_status_t zhulong_plugin_get_api(
    uint32_t requested_version,
    void* out_api_table,
    size_t table_size
);

#ifdef __cplusplus
}
#endif

#endif /* ZHULONG_PLUGIN_H */
