# 算法插件 SDK、公共运行库与通用 Runner：技术设计

## 1. 总体架构与数据流

本任务构建算法插件的第一道防线，定义清晰的 C ABI 边界、提供极其丝滑的 C++17 SDK，并实现通用 Runner 与公共算法运行库。

```txt
外部插件开发域 (.so 内部)                    通用原生 Runner 域 (进程宿主)
┌─────────────────────────────────┐          ┌───────────────────────────────────┐
│ 用户感知算法代码 (如 YOLO)      │          │ zhulong_algo_runner 进程          │
│   #include <zhulong/algo.hpp>   │          │                                   │
│   Model, Session, Tensor, ops   │          │ 1. dlopen("plugin.so")            │
└────────────────┬────────────────┘          │ 2. get_api(&api_v1)               │
                 │ 静态内联胶水               │ 3. api->model_create(&model_grp)  │
                 ▼                           │ 4. api->worker_create(&worker)    │
┌─────────────────────────────────┐          │ 5. api->process(worker, ...)      │
│ 纯 C ABI 导出 (plugin.h)        │ ◄─────── │ 6. api->result_release(result)    │
│   zhulong_plugin_get_api()      │          └─────────────────┬─────────────────┘
└─────────────────────────────────┘                            │
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │ libzhulong_algo_runtime.so        │
                                             │  ├─ IInferenceBackend (CPU 桩)    │
                                             │  ├─ IVisionBackend (CPU 优化桩)   │
                                             │  └─ NMS / 坐标映射工具            │
                                             └───────────────────────────────────┘
```

---

## 2. 纯 C ABI 契约设计 (`native/sdk/include/zhulong/plugin.h`)

### 2.1 基础类型与错误码
```c
#ifndef ZHULONG_PLUGIN_H
#define ZHULONG_PLUGIN_H
#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef int32_t zhulong_status_t;

enum {
    ZHULONG_PLUGIN_OK = 0,
    ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT = -1,
    ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY = -2,
    ZHULONG_PLUGIN_ERR_NOT_SUPPORTED = -3,
    ZHULONG_PLUGIN_ERR_INTERNAL = -99
};

// 不透明句柄
typedef struct zhulong_model_group_t* zhulong_model_group_h;
typedef struct zhulong_worker_ctx_t*  zhulong_worker_ctx_h;
typedef struct zhulong_task_ctx_t*    zhulong_task_ctx_h;
```

### 2.2 核心函数表 `zhulong_plugin_api_v1`
```c
typedef struct zhulong_plugin_api_v1 {
    uint32_t struct_size;
    uint32_t api_version; // 1

    // 1. 模型资源组生命周期
    zhulong_status_t (*model_create)(const char* config_json, zhulong_model_group_h* out_model);
    void             (*model_destroy)(zhulong_model_group_h model);

    // 2. Worker 执行上下文生命周期
    zhulong_status_t (*worker_create)(zhulong_model_group_h model, uint32_t worker_id, zhulong_worker_ctx_h* out_worker);
    void             (*worker_destroy)(zhulong_worker_ctx_h worker);

    // 3. 任务状态绑定 (单摄像机时序状态)
    zhulong_status_t (*task_open)(zhulong_worker_ctx_h worker, uint64_t task_id, zhulong_task_ctx_h* out_task);
    void             (*task_close)(zhulong_task_ctx_h task);

    // 4. 同步推理与结果释放
    zhulong_status_t (*process)(zhulong_task_ctx_h task, const zhulong_frame_view* frame, zhulong_inference_result** out_result);
    void             (*result_release)(zhulong_inference_result* result);
} zhulong_plugin_api_v1;

// 唯一导出的 C 符号
zhulong_status_t zhulong_plugin_get_api(uint32_t requested_version, void* out_api_table, size_t table_size);
```

---

## 3. 现代 C++17 SDK 设计 (`native/sdk/include/zhulong/algo.hpp`)

`zhulong/algo.hpp` 采用 Header-only 模式，内部封装：
1. **`zhulong::Tensor`**：轻量张量容器（维度 shape、类型 dtype、数据指针、所有权管理）；
2. **`zhulong::Model`**：只读模型资产包装器，通过内部 `IInferenceBackend` 加载不可变权重；
3. **`zhulong::Session`**：单个 Worker 的推理会话，绑定特定推理上下文，调用 `forward()`；
4. **`zhulong::ops`**：
   - `letterbox(image, target_size, format)`：调用 `IVisionBackend` 进行硬件/CPU 缩放补边；
   - `nms(boxes, conf_thresh, iou_thresh)`：纯 C++ 优化版非极大值抑制。
5. **导出宏 `ZHULONG_REGISTER_PLUGIN(PluginClass)`**：
   - 自动生成符合 `zhulong_plugin_api_v1` 的纯 C ABI 入口；
   - 内部包含 `try-catch (...)` 异常防爆门，全自动将 C++ 异常安全转换为 `ZHULONG_PLUGIN_ERR_INTERNAL`。

---

## 4. 公共运行库硬件抽象层 (HAL) 架构

在 `libzhulong_algo_runtime.so` 中定义中立接口：
- `class IInferenceBackend`：
  - `load_model(config)` ➔ `std::shared_ptr<ModelAsset>`
  - `create_session(model_asset, worker_id)` ➔ `std::unique_ptr<SessionContext>`
  - `forward(session, inputs, outputs)`
- `class IVisionBackend`：
  - `letterbox(src, dst, params)`
  - `resize(src, dst)`
  - `cvt_color(src, dst, code)`

本阶段实现 **`CpuInferenceBackend`** 与 **`CpuVisionBackend`**：
- 前者使用内置合成算子与测试夹具模拟前向传播，输出确定性测试特征；
- 后者提供标准的 CPU 双线性插值与颜色转换，集成基本的 SIMD 优化，作为基准对比与无硬件环境的保底手段。

---

## 5. 通用 Runner 与独立黑盒验包 CLI

`zhulong_algo_runner` 提供两种运行模式：
1. **工作模式 (Worker Mode)**：
   - `zhulong_algo_runner --plugin <so_path> --workers <N> --config <json>`；
   - 初始化工作线程池，监听标准控制事件并调度推理。
2. **独立黑盒验包模式 (Verify Mode)**：
   - `zhulong_algo_runner --verify-plugin <so_path> --sample <file_path>`；
   - 流程：
     1. `dlopen` 校验与 ABI 版本协商；
     2. 创建测试模型与单个 Worker；
     3. 样本文件加载器读取样本并构造 `zhulong_frame_view`；
     4. 执行一次标准 `process` 调用；
     5. 校验返回结果结构体有效性、非野指针；
     6. 调用 `result_release`，析构 Worker 与 Model；
     7. 输出 JSON 格式验包报告并退出。
