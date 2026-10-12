# 算法插件系统契约与 SDK 规范 (Algorithm Plugin Architecture)

> 纯 C ABI 隔离、现代 C++17 SDK 门面、公共算法运行库 HAL 与通用 Runner。

---

## 1. 架构原则与边界约束

1. **一包一进程 (One Package, One Process)**：每个算法包运行在独立的 Native Runner 子进程中，故障隔离，崩溃不拖垮主媒体与 Go 服务。
2. **纯 C ABI 物理隔离**：插件动态库必须导出纯 C ABI（`zhulong_plugin_api_v1`），禁止跨 `.so` 边界暴露 C++ class、虚表、STL 或厂商专有 SDK 上下文。
3. **C++17 Header-only SDK 门面**：第三方开发者统一引入 `zhulong/algo.hpp`，通过 RAII、智能指针与操作符链式调用编写现代 C++17 算法，避免手动管理底层句柄。
4. **两层硬件抽象层 (HAL)**：公共运行库 `libzhulong_algo_runtime.so` 封装 `IInferenceBackend` 与 `IVisionBackend`，屏蔽不同加速器（RKNN、CANN、TensorRT、CPU）的差异。
5. **三层资源解耦**：
   - **Model Group**：全局只读模型权重，多线程共享；
   - **Worker Context**：线程绑定的执行会话与工作区内存，多线程并发互不干扰；
   - **Task State**：单路摄像机时序状态（跟踪器、平滑器），通过 task ID 独立存取。

---

## 2. 纯 C ABI 规范 (`native/sdk/include/zhulong/plugin.h`)

所有插件通过 `zhulong_plugin_get_api` 导出函数表：

```c
typedef struct zhulong_plugin_api_v1 {
    uint32_t api_version;      // 必须为 1
    const char* plugin_name;   // 插件唯一名称
    const char* plugin_version;// 语义化版本号

    zhulong_status_t (*model_create)(const char* config_json, zhulong_model_group_h* out_model);
    void (*model_destroy)(zhulong_model_group_h model);

    zhulong_status_t (*worker_create)(zhulong_model_group_h model, uint32_t worker_idx, zhulong_worker_ctx_h* out_worker);
    void (*worker_destroy)(zhulong_worker_ctx_h worker);

    zhulong_status_t (*task_open)(zhulong_worker_ctx_h worker, uint64_t task_id, zhulong_task_ctx_h* out_task);
    void (*task_close)(zhulong_task_ctx_h task);

    zhulong_status_t (*process)(zhulong_task_ctx_h task, const zhulong_frame_view* frame, zhulong_inference_result** out_result);
    void (*result_release)(zhulong_inference_result* result);
} zhulong_plugin_api_v1;
```

### 异常防爆门守则
导出宏在最外层以 `try { ... } catch (...) { return ZHULONG_PLUGIN_ERR_INTERNAL; }` 严格包裹，绝对禁止 C++ 异常穿越动态库边界。

---

## 3. 现代 C++17 SDK 门面 (`native/sdk/include/zhulong/algo.hpp`)

算法实现仅需实现继承接口，并通过宏注册导出：

```cpp
#include <zhulong/algo.hpp>

class MyDetector : public zhulong::IDetector {
public:
    bool init(const std::string& config_json) override { ... }
    std::vector<zhulong::BBox> process(uint64_t task_id, const zhulong::Image& img) override {
        // 利用 zhulong::ops 进行 Letterbox 与 NMS 算子处理
        ...
    }
};

ZHULONG_REGISTER_DETECTOR_PLUGIN(MyDetector, "MyDetector", "1.0.0")
```

---

## 4. 独立黑盒验包 CLI 工具 (`zhulong_algo_runner`)

验包命令：
```bash
zhulong_algo_runner --verify-plugin <path_to_plugin.so> --sample <test_image_or_mock>
```
输出严格 JSON 结构：
```json
{"step":"start","plugin":"..."}
{"step":"abi_verified","name":"...","version":"..."}
{"status":"passed","sample":"...","result_type":1,"count":2}
```
退出码：`0` 表示 ABI、模型加载、Worker 创建、Task 打开、单次推理与内存释放全部符合规范。
