# 算法包开发与交付规范 (Algorithm Package Specification)

> Zhulong 视觉计算平台算法包工程标准：物理目录拓扑、`manifest.json` 权威元数据、安全打包与本地黑盒验包准入门禁。

---

## 1. 算法包物理目录拓扑 (Package Topology)

算法包必须采用**扁平化归档**（根目录下直接包含约定文件，**严禁外层包裹随机命名的顶层文件夹**）：

```txt
<package_name>.tar.gz (或 .zip)
├── manifest.json       # [必须] 算法元数据声明与算力资源规格清单 (权威定义见 §2)
├── plugin.so           # [必须] 导出纯 C ABI 的推理动态链接库 (权限必须为 0755)
├── sample.jpg          # [必须] 用于准入黑盒测试的真实前向推理样本图
├── models/             # [可选] 算法只读权重模型 (.onnx, .rknn, .om, .engine 等)
├── config.schema.json  # [可选] 算法超参数配置的 JSON Schema (缺省为零参算法)
└── .env                # [可选] 算法运行时本地环境变量覆写文件 (见 §3)
```

### 1.1 归档与打包铁律 (Packaging Rules)

1. **归档格式**：仅支持 `.tar.gz`（魔数 `0x1F, 0x8B`）或 `.zip`（魔数 `0x50, 0x4B, 0x03, 0x04`）。
2. **扁平根路径**：归档根目录下必须直接暴露 `manifest.json` 与 `plugin.so`。严禁打包为 `dist/` 或 `<package_name>/` 嵌套目录。
3. **文件权限**：动态库 `plugin.so` 在 Linux 下必须具备可执行权限（`chmod 0755 plugin.so`）；Windows 打包 zip 时平台服务会在解压后自动补正为 `0755`。
4. **安全与体积硬门禁**：
   - 压缩包上传体积 ≤ **500MB**（超限触发 HTTP 413 熔断）；
   - 解压后文件总数 ≤ **1,000 个**（防御解压炸弹）；
   - 解压后展开总容量 ≤ **1GB**（防御解压炸弹）；
   - **绝对路径安全 (Zip-Slip 防护)**：归档内所有条目路径严禁包含 `../` 相对路径穿越或根绝对路径，一旦检出直接拒绝并告警。

---

## 2. `manifest.json` 元数据权威规范

算法包根目录必须提供 `manifest.json`，声明算法标识、适用平台、算力预算与任务形态：

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

### 2.1 字段权威约束

| 字段 | 类型 | 必填 | 约束与取值范围 | 说明 |
| :--- | :--- | :---: | :--- | :--- |
| `id` | string | 是 | 正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` | 算法全局唯一机器标识，用于物理路径拼接，严禁空格与路径字符 |
| `name` | string | 是 | 字符串，UTF-8 字节长度 `1 ~ 32` | 展示名称；超过 32 字节将在 IPC 握手包定长缓冲截断并被拒 |
| `version` | string | 是 | 正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` | 语义化版本号（如 `1.0.0`） |
| `description` | string | 否 | 字符串，≤ 256 字符 | 算法功能简短描述 |
| `task_type` | string | 是 | 枚举：`detection` \| `classification` \| `person_structure` | 算法任务类型，决定推理结果的数据结构 |
| `category` | string | 是 | 枚举：`safety` \| `security` \| `fire` \| `traffic` \| `general` | 应用行业分类 |
| `platform.arch` | string | 是 | 枚举：`x86_64` \| `aarch64` | 目标硬件 CPU 指令集架构 |
| `platform.target` | string | 是 | 枚举：`cpu` \| `rknn` \| `cann` \| `tensorrt` | 硬件推理加速后端 |
| `resources.compute_cost` | int | 是 | 整数 `1 ~ 1000` | 单帧基准算力消耗点数（全平台总预算池为 1,000 点） |
| `resources.memory_mb` | int | 是 | 整数 `≥ 16` | 预估最大常驻内存（MB） |

---

## 3. 环境变量 `.env` 配置规范

算法包根目录下可包含可选的 `.env` 配置文件，用于动态调节底层推理驱动（如 RKNN、OpenMP）与模型超参数：

```ini
OMP_NUM_THREADS=4
RKNN_CORE_MASK=all
CONFIDENCE_THRESHOLD=0.45
NMS_THRESHOLD=0.50
```

- 采用标准 `KEY=VALUE` 格式，支持 `#` 注释；
- 平台拉起算法子进程时会自动解析并将变量注入至环境变量，算法代码通过 `std::getenv()` 读取。

---

## 4. 动态库开发契约与 Native 规范索引

`plugin.so` 是遵循纯 C ABI 的共享库，由官方独立沙箱子进程（Runner）加载执行。核心开发规约如下（详细 C/C++ 契约请参阅对应原生规范，本处不重复定义）：

1. **纯 C ABI 与 SDK 门面**：
   - 必须通过 `zhulong_plugin_get_api` 导出纯 C 函数表；
   - 推荐使用 C++17 Header-only SDK（`zhulong/algo.hpp`）宏注册插件；
   - 详见 👉 [算法插件系统契约与 SDK 规范](../native/algo-plugin-contract.md)。
2. **异构硬件加速后端**：
   - RKNN 推理与 RGA 视觉前处理等特定芯片加速规范；
   - 详见 👉 [原生硬件加速后端规范](../native/hardware-backends.md)。
3. **坐标系与结果契约**：
   - 所有检测框坐标必须恒定归一化至 `[0.0, 1.0]`（`[x1, y1, x2, y2]`），严禁使用绝对像素值；
   - 未检出目标时必须返回空数组（`count = 0`），严禁返回空指针；
   - 异常绝对禁止穿越 C ABI 边界（必须全量 `try-catch` 拦截）。

---

## 5. 本地黑盒验包与自测门禁 (Verification Gate)

在向平台交付算法包之前，开发者必须在本地使用平台提供的 `zhulong_algo_runner` CLI 进行黑盒自测：

```bash
zhulong_algo_runner --verify-plugin plugin.so --sample sample.jpg
```

退出码为 `0` 且终端输出 `{"status":"passed", ...}` 时方可通过准入校验。

### 平台准入拦截错误代码对照表

| 平台拦截错误码 | 触发原因 | 开发者整改指引 |
| :--- | :--- | :--- |
| `ZIP_SLIP_ATTACK_DETECTED` | 压缩包内含有 `../` 相对路径或绝对路径 | 重新打包，确保所有文件使用相对根目录路径 |
| `DECOMPRESSION_BOMB_DETECTED` | 文件数 > 1,000 或解压总大小 > 1GB | 精简模型与依赖库体积，移除无用中间文件 |
| `ALGO_PLATFORM_MISMATCH` | `platform.arch`/`target` 与运行宿主配置不符 | 核验交叉编译工具链与 `manifest.json` 平台声明 |
| `INVALID_MANIFEST_TOKEN` | `id` / `version` 包含非法字符，或 `name` 超过 32 字节 | 匹配正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` 并缩短 `name` |
| `ALGO_VERIFICATION_FAILED` | `zhulong_algo_runner` 验包退出码非 0 | 使用 `sample.jpg` 在本地复现排查崩溃与内存问题 |

---

## 6. 控制面分层与模块单一职责 (Module Boundaries & Responsibilities)

1. **核心单一职责**：`internal/algorithm` 专注且仅负责：
   - **算法包制品资产管理**（解压、Zip-Slip 与炸弹防御、元数据解析、黑盒验包准入门禁、持久化）；
   - **Runner 外部沙箱子进程监督**（环境变量注入、拉起与保活、指数退避、CrashLoop 熔断及版本自动回滚）。
2. **分析任务解耦**：分析任务（CRUD、配置校验、算力预算检查与调度）完全归属 `internal/task` 模块。`algorithm` 仅实现 `task.AlgorithmSpecProvider`（供任务校验算法规格）与 `task.TaskChangeListener`（接收任务变更通知以触发防抖热重启）。兼容路由 `/api/v1/algorithms/tasks` 由 `task.Handler` 统一委托承载。
3. **推理数据与协议下沉**：
   - 通用 AI 推理模型（`InferenceResult`、`DetectionItem`、`PersonItem` 等）收敛于 `internal/aitypes`，解耦 `camera` 与 `record` 对 `algorithm` 的依赖；
   - 16 字节定长二进制帧头编解码与协议常量收敛于 `internal/algoipc`。
4. **错误处理与 apperr 闭环**：`Service` 统一使用 `apperr.Error` 分类暴露错误（`KindInvalid` 对应 422 校验失败，`KindConflict` 对应 409 活跃冲突，`KindNotFound` 对应 404），由 `httputil.WriteError(c, err)` 统一写回并自动完成多语言翻译。


