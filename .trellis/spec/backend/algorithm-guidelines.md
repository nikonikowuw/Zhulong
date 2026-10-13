# 算法包开发与交付规范 (Algorithm Package Specification)

> 规定 Zhulong 视觉计算平台算法包的工程标准：物理目录拓扑、元数据契约、纯 C ABI/C++17 SDK、图像输入与结果输出规范、安全打包与本地黑盒验包验收门禁。

---

## 1. 适用范围与开发角色 (Scope & Roles)

本规范面向所有**算法开发者、第三方模型交付团队及算法集成工程师**。

所有向 Zhulong 平台交付的视觉计算算法包（检测、分类、人脸/人体分析等），必须严格遵循本规范进行代码编写、模型封装、配置声明与归档打包，以确保通过平台的自动化安全准入门禁并在独立子进程中稳定运行。

---

## 2. 算法包物理目录拓扑 (Package Topology)

算法包必须采用**扁平化归档**（根目录下直接包含约定文件，**严禁外层包裹随机命名的顶层文件夹**）：

```txt
<package_name>.tar.gz (或 .zip)
├── manifest.json       # [必须] 算法元数据声明与算力资源规格清单
├── plugin.so           # [必须] 导出纯 C ABI 的推理动态链接库 (权限必须为 0755)
├── sample.jpg          # [必须] 用于准入黑盒测试的真实前向推理样本图
├── models/             # [可选] 算法只读权重模型 (.onnx, .rknn, .om, .engine 等)
├── config.schema.json  # [可选] 算法超参数配置的 JSON Schema (缺省为零参算法)
└── .env                # [可选] 算法运行时本地环境变量覆写文件
```

### 2.1 归档与打包铁律 (Packaging Rules)

1. **归档格式**：仅支持 `.tar.gz`（前两字节魔数 `0x1F, 0x8B`）或 `.zip`（前四字节魔数 `0x50, 0x4B, 0x03, 0x04` 即 `PK..`）。
2. **扁平根路径**：归档根目录下必须直接暴露 `manifest.json` 与 `plugin.so`。严禁先打一个 `dist/` 或 `<package_name>/` 文件夹再压缩。
3. **文件权限**：动态库 `plugin.so` 在 Linux 下必须具备可执行权限（`chmod 0755 plugin.so`）。若在 Windows 打包 zip，平台服务会在解压后自动补偿修正为 `0755`，防止运行时 `dlopen` 报权限拒绝。
4. **安全与体积硬门禁**：
   - 压缩包上传体积 ≤ **500MB**（超限立即触发 `413 Payload Too Large` 熔断）；
   - 解压后文件总数 ≤ **1,000 个**（防御解压炸弹）；
   - 解压后展开总容量 ≤ **1GB**（防御解压炸弹）；
   - **绝对路径安全 (Zip-Slip 防护)**：归档内所有条目路径严禁包含 `../` 相对路径穿越，一旦检出直接判定为恶意包拒绝并告警。

---

## 3. `manifest.json` 元数据声明规范

算法包根目录必须提供 `manifest.json`，用于声明算法标识、适用平台、算力预算与任务形态：

```json
{
  "id": "safety_helmet_detector",
  "name": "安全帽佩戴检测算法",
  "version": "1.0.0",
  "description": "基于 Yolov8-Nano 的施工现场安全帽佩戴实时检测",
  "task_type": "detection",
  "category": "safety",
  "platform": {
    "arch": "x86_64",
    "target": "cpu"
  },
  "resources": {
    "compute_cost": 20,
    "memory_mb": 128
  }
}
```

### 3.1 字段权威约束

| 字段 | 类型 | 必填 | 约束与取值范围 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | string | 是 | 正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` | 算法全局唯一机器标识，参与物理安装路径拼接，严禁空格、斜杠与特殊字符 |
| `name` | string | 是 | 字符串，UTF-8 字节长度 `1 ~ 32` | 人类可读展示名称。超过 32 字节将在 IPC 握手包定长缓冲截断并被拒 |
| `version` | string | 是 | 正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` | 语义化版本号（如 `1.0.0`），严禁非法路径字符 |
| `description` | string | 否 | 字符串，≤ 256 字符 | 算法功能简短描述 |
| `task_type` | string | 是 | 枚举：`detection` \| `classification` \| `person_structure` | 算法任务类型，决定推理结果的数据结构 |
| `category` | string | 是 | 枚举：`safety` \| `security` \| `fire` \| `traffic` \| `general` | 应用行业与场景分类 |
| `platform.arch` | string | 是 | 枚举：`x86_64` \| `aarch64` | 目标硬件 CPU 指令集架构 |
| `platform.target` | string | 是 | 枚举：`cpu` \| `rknn` \| `cann` \| `tensorrt` | 硬件推理加速后端 |
| `resources.compute_cost` | int | 是 | 整数 `1 ~ 1000` | 单帧基准算力消耗点数（全平台总预算池为 1,000 点） |
| `resources.memory_mb` | int | 是 | 整数 `≥ 16` | 预估最大常驻内存（MB） |

---

## 4. 插件动态库开发与 C ABI 契约 (`plugin.so`)

### 4.1 C++17 Header-only SDK 快速开发 (`zhulong/algo.hpp`)

推荐使用平台官方 C++17 SDK，通过 RAII 和强类型接口快速实现检测器或分类器：

```cpp
#include <zhulong/algo.hpp>

class HelmetDetector {
public:
    // 初始化：加载模型权重与配置 (只读模型建议放在 models/ 目录下)
    zhulong_status_t init(const std::string& config_json) {
        return load_model("models/helmet_v8n.onnx");
    }

    // 会话创建：多 Worker 并发隔离支持
    zhulong::Session create_session(uint32_t worker_id) {
        return zhulong::Session(worker_id);
    }

    // 核心前向推理：接收图像视图，返回检测框列表
    std::vector<zhulong::Detection> detect(zhulong::Session& session, const zhulong::Image& img) {
        std::vector<zhulong::Detection> results;
        // 执行模型前向推理...
        results.push_back({
            .box = {0.2f, 0.15f, 0.5f, 0.6f}, // 归一化坐标 [x_min, y_min, x_max, y_max]
            .label = "helmet",
            .score = 0.92f
        });
        return results;
    }
};

// 注册插件导出入口 (SDK 宏自动处理 zhulong_plugin_api_v1 纯 C ABI 与异常隔离)
ZHULONG_REGISTER_DETECTOR_PLUGIN(HelmetDetector, "HelmetDetector", "1.0.0")
```

若开发分类算法，引入宏 `ZHULONG_REGISTER_CLASSIFIER_PLUGIN`，并实现 `std::vector<zhulong::Classification> classify(...)`。

### 4.2 符号可见性与隔离原则 (Symbol Visibility)

1. **单一导出入口**：动态库编译时必须开启默认符号隐藏（`-fvisibility=hidden`），仅显式导出 `zhulong_plugin_get_api` 入口函数；
2. **符号防污染**：严禁暴露 OpenCV、ONNXRuntime、RKNN 等第三方库的内部全局符号，必须使用 GNU ld version script 将其它定义符号设为 local，防止多算法同进程/容器加载时产生符号冲突（ODR 违规）。

### 4.3 异常防爆门铁律 (Exception Safety)

**C++ 异常绝对禁止穿越动态库边界！**
所有导出接口内部必须以 `try { ... } catch (...)` 严密包裹。任何未捕获的 C++ 异常一旦逃逸至纯 C 接口边界，将直接触发子进程 `std::terminate` 崩溃，被平台看门狗判定为致命错误。

### 4.4 内存所有权准则 (Memory Ownership)

**谁分配，谁释放。**
算法插件内部创建的任何结果缓冲区（如检测框结构体数组），必须且只能通过插件自身导出的 `result_release` 回调函数进行回收。平台 Runner 严禁对插件指针执行裸 `free()`。

---

## 5. 图像输入与推理结果输出标准

### 5.1 图像输入格式契约 (`zhulong_frame_view`)

- **像素排布**：当前支持 packed `RGB24` / `BGR24` 三通道图像；
- **步长寻址**：必须遵循 `strides[0]` 步长进行逐行偏移计算（行首地址为 `src + y * src_stride`），严禁假定图像内存紧密连续而直接按 `y * width * 3` 越界寻址；
- **只读保证**：输入帧位于宿主共享内存只读映射区（`PROT_READ`），算法逻辑**严禁尝试原地修改原图像素**。

### 5.2 推理结果四大黄金铁律

1. **坐标归一化 `[0.0, 1.0]`**：
   所有检测框（`[x_min, y_min, x_max, y_max]`）和关键点坐标必须归一化为 `0.0 ~ 1.0` 浮点数，彻底解耦输入分辨率与前端播放窗口拉伸；
2. **单一语义化标签**：
   检测项仅输出 `label` 语义字符串（如 `"helmet"`、`"no_helmet"`）和 `score` 置信度，严禁使用数字 `class_id`，模型重新训练调整类别索引不破坏业务联动；
3. **时钟与帧保真 (PTS)**：
   推理事件强绑定输入图像的 `pts`（呈现时间戳），保障前端音画与 AI 检测框毫秒级同步；
4. **零结果空数组约定**：
   画面中未检测到目标时，必须返回有效空数组（长度为 0），**严禁返回空指针或 null**。

---

## 6. 环境变量 `.env` 配置规范

算法包根目录下可包含可选的 `.env` 配置文件，支持免重新编译动态调节底层推理驱动（如 RKNN、OpenMP）与模型超参数：

```ini
# 模型推理工作线程数
OMP_NUM_THREADS=4
# NPU 核心绑定配置 (RKNN 平台可用 0, 1, 2, all)
RKNN_CORE_MASK=all
# 算法内部置信度过滤阈值
CONFIDENCE_THRESHOLD=0.45
# NMS IOU 阈值
NMS_THRESHOLD=0.50
```

- 必须采用标准 `KEY=VALUE` 格式，支持 `#` 注释行；
- 平台启动算法子进程时会自动解析并将变量合并注入至运行环境，算法代码可通过 `std::getenv()` 读取。

---

## 7. 本地黑盒验包与自测门禁 (Verification Gate)

在向平台上传交付算法包之前，开发者必须在本地使用平台提供的 `zhulong_algo_runner` CLI 进行黑盒自测。

### 7.1 本地验包命令

```bash
zhulong_algo_runner --verify-plugin plugin.so --sample sample.jpg
```

### 7.2 验包流水线检查项

Runner 将在独立沙箱子进程中依次执行以下测试：

1. **ABI 规范性探测**：验证动态库导出符号与 `zhulong_plugin_api_v1` 函数表结构尺寸；
2. **模型加载 (`model_create`)**：加载权重，检验内存与初始化流程；
3. **工作会话创建 (`worker_create`)**：验证多 Worker 隔离与上下文构建；
4. **任务通道建立 (`task_open`)**：开启模拟单路视频分析任务；
5. **前向推理 (`process`)**：输入 `sample.jpg` 执行实际前向计算，核验输出格式是否与 `task_type` 匹配；
6. **资源销毁 (`result_release`, `model_destroy`)**：验证内存无越界与悬挂指针。

只有命令退出码为 `0` 且终端输出 `{"status":"passed", ...}` 时，算法包才具备交付准入资格！

### 7.3 常见准入被拒错误代码对照表

| 平台拦截错误码 | 触发原因 | 开发者整改指引 |
| --- | --- | --- |
| `ZIP_SLIP_ATTACK_DETECTED` | 压缩包内含有 `../` 路径段或绝对路径 | 重新打包，确保所有文件使用相对根目录路径 |
| `DECOMPRESSION_BOMB_DETECTED` | 文件数 > 1,000 或解压总大小 > 1GB | 精简模型与依赖库体积，移除无用的训练中间权重 |
| `ALGO_PLATFORM_MISMATCH` | `platform.arch`/`target` 与运行设备不符 | 确认交叉编译工具链及 manifest 平台声明 |
| `INVALID_MANIFEST_TOKEN` | `id` 或 `version` 含非法字符，或 `name` 超过 32 字节 | 检查正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` 并缩短 `name` |
| `ALGO_VERIFICATION_FAILED` | `zhulong_algo_runner` 验包退出码非 0 | 使用 sample.jpg 在本地重现并排查崩溃与内存问题 |

---

## 8. 典型反例与正确范例 (Wrong vs Correct)

### 反例 1：打包时目录结构嵌套

```bash
# ❌ 错误：在外层文件夹打包，导致解压后多出一层随机/顶层目录
tar -czvf my_detector.tar.gz my_detector/

# ✅ 正确：进入目录内部打包，保证归档根路径直接为约定文件
cd my_detector && tar -czvf ../my_detector.tar.gz *
```

### 反例 2：检测框坐标使用像素绝对值

```cpp
// ❌ 错误：输出绝对像素坐标，摄像头分辨率或播放器缩放时严重错位
detection.box = {192.0f, 108.0f, 384.0f, 216.0f};

// ✅ 正确：输出 [0.0, 1.0] 归一化浮点数
detection.box = {0.1f, 0.1f, 0.2f, 0.2f};
```

### 反例 3：未捕获异常导致子进程崩溃

```cpp
// ❌ 错误：内部逻辑抛出 std::runtime_error，未捕获直接穿透 C 边界导致子进程崩溃
int process(...) {
    if (!tensor) throw std::runtime_error("null tensor");
    return 0;
}

// ✅ 正确：严密捕获所有 C++ 异常，转换为错误状态码
int process(...) {
    try {
        if (!tensor) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        // ...
        return ZHULONG_PLUGIN_SUCCESS;
    } catch (const std::exception& e) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    } catch (...) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}
```
