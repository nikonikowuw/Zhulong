# Rockchip 首期后端：上下文与权重共享证据

状态：仅做源码/API 研究与架构规划。未进行板端推理、兼容性、内存或性能验证，未修改产品代码。

## 1. 平台与证据边界

- 用户本轮确认首期平台为 Rockchip。
- `.trellis/tasks/10-05-native-ffmpeg-ingestion/prd.md:47-50` 已记录首个目标为 RK3568，用户记忆中镜像是 Debian，板卡暂不在手边，具体架构、内核和运行库未实测；该任务没有接入 RKNN SDK。
- 当前仓库没有 `.agents/context/rknn-context/` 或 `.agents/rknn-model-context.md`；在排除 `.git`、构建目录和前端依赖的源码扫描中未发现 `rknn_api.h` 或 `librknnrt.so`。
- 架构讨论沿用既有 RK3568 参考约束；不将其提升为板端认证结果，不推定用户态是 arm64，也不套用 RK3588 三核分配方案。
- Runtime、Toolkit2、RKNPU 驱动版本和模型量化类型均未知。
- Model Conversion Evidence Gate：`not-applicable`，本轮只讨论上下文/API/进程调度，不决定模型预处理、tensor layout、量化后处理或生产零拷贝参数。此状态不等于转换证据已齐备。

## 2. 官方源码证据

通过 GitHub Contents API 获取并解码官方头文件固定快照；未将远程头文件替换进项目构建。raw.githubusercontent.com 直连获取失败，GitHub API 获取成功。

- 仓库：`airockchip/rknn-toolkit2`
- 固定 ref：`59a913d172e7f5ff03c9076e2ec7b1b1288ffd08`
- 路径：`rknpu2/runtime/Linux/librknn_api/include/rknn_api.h`
- SHA-256：`c48e11a6f41b451a5fd1e4ad774ea60252d3d94f78bee9b21ea3d21b21deba9a`
- 来源：https://github.com/airockchip/rknn-toolkit2/blob/59a913d172e7f5ff03c9076e2ec7b1b1288ffd08/rknpu2/runtime/Linux/librknn_api/include/rknn_api.h

| 官方头文件位置 | 源码事实 | 不可推定的内容 |
| --- | --- | --- |
| L47-51 | `RKNN_FLAG_MEM_ALLOC_OUTSIDE` 描述外部分配；`RKNN_FLAG_SHARE_WEIGHT_MEM` 注释为同网络结构权重共享 | 不证明任何目标 Runtime 已支持，也不能只按网络结构相同就允许不同权重内容互换 |
| L491-501 | `int rknn_dup_context(rknn_context* context_in, rknn_context* context_out)` | 声明/简短注释没有保证共享权重、具体节省量或销毁次序 |
| L723-733 | `int rknn_set_weight_mem(rknn_context ctx, rknn_tensor_mem *mem)` | 不能据此猜测外部分配、初始化 flag、权重填充及绑定的完整合法组合 |
| L736-746 | `int rknn_set_internal_mem(rknn_context ctx, rknn_tensor_mem *mem)` | 多个并发 worker 不能因为存在该接口就随意共享可变内部 workspace |
| L144、L341-344 | `RKNN_QUERY_SDK_VERSION` 与 `rknn_sdk_version` 的 `api_version`、`drv_version` | 不代表当前板子的实际版本，需成功初始化后查询并记录 |
| L146、L349-356 | `RKNN_QUERY_MEM_SIZE`，含 `total_weight_size`、`total_internal_size`、`total_dma_allocated_size` 等字段 | 不应把多个上下文的逻辑权重大小直接相加当作实际独立物理分配，也不等于完整系统内存 |

## 3. 首期实现候选（不是已批准实现）

对同一个模型资源组：在包进程中创建根上下文，再评估 `rknn_dup_context(&root, &worker)` 派生独立 worker 上下文，每个 worker 独占其输入、输出与可变执行状态。两个参数都是指针，不能传裸 `root` 值。

把上下文复制视为待验证候选，而不是权重共享的证明：

1. 使用版本匹配的 RKNN 头文件、Runtime、模型和驱动，核实官方上下文生命周期合同；根上下文保守保留至所有依赖 worker 回收完成。
2. 对比重复 `rknn_init` 与根上下文加 `rknn_dup_context`；前者仅作为对照，不是悄悄替代用户共享权重要求的回退。
3. 校验输出一致性、并发稳定性、创建失败回滚与销毁行为；只释放成功创建且不再有在途工作的上下文。
4. 若复制方案未满足共享目标，再按版本匹配文档/官方示例评估显式共享权重内存路线。不未经验证地拼接 flag 和接口。
5. 若目标栈无法满足共享目标，向用户提出产品取舍，不静默复制多份权重并声称完成。

资源由工作进程内 RKNN Runtime/后端适配器按对应接口创建与销毁；宿主只管理进程和 IPC，不释放 worker 的 RKNN 指针。Runtime 分配的普通推理输出按对应输出释放接口归还；上下文关联 tensor memory 在设备任务结束后按对应内存 API 释放，遵守版本合同。

## 4. RK3568 调度约束

本地 Rockchip 调度参考 `multi-model-scheduling.md`、`soc-matrix.md` 给出的常见拓扑：RK3568 单 NPU 核，RK3576 双核，RK3588 三核。此处只用 RK3568 的单执行资源约束进行保守规划，仍需实际板端能力验证。

- 多个 worker/context 不等于多个 NPU 核同时执行。RK3568 的潜在收益来自 CPU/预处理/后处理与 NPU 工作重叠，不能承诺线程数倍增吞吐。
- 初步测量点可设 1、2、4 个 worker，不把它们写成产品默认值。
- 所有算法包共享同一设备；每包有界线程池之外，还需考虑设备级总并发与内存预算，避免包数乘以线程数造成过量排队。
- `RKNN_FLAG_ASYNC_MASK` 不应当作通用多线程开关。具体前帧输出语义必须对照目标版本；本轮不选用它。

## 5. 验证与后续证据

- 架构阶段可继续确定包职责、C ABI/IPC 边界、模型资源/执行实例抽象、队列和生命周期。
- 板端到手后先记录 `compatible`、内核、用户态架构与 Runtime/驱动查询结果，再补 Toolkit2/模型来源。
- 内存测试结合 RSS/PSS、Runtime 查询、设备/DMA 分配指标，不仅看模型文件读取次数。
- 固定模型与负载，对比单/多 worker 的吞吐、端到端 P95、队列等待、正确性、FD/内存增长以及反复启动停止。
- 不直接在本开发机执行板端诊断并冒充 RK3568 证据。

## 6. 后续收敛状态

首期平台已确认且 SDK 未知已记录，不重复询问既有任务已确认的 RK3568 事实。用户随后选择 A（感知算法包）：插件负责前处理/模型执行/后处理，公共层负责通用跟踪/规则/事件，算法本身必需的时序状态仍可留在包内。用户已选择包级重启升级，允许该包短暂分析中断，避免首期引入新旧模型同时驻留的峰值内存。用户已选择意外崩溃后的有界自动恢复，并确认升级启动失败后受控回滚到上一可用版本。产品需求与技术建议已汇总至 `../prd.md`、`../design.md`，分阶段验收见 `../implement.md`；SDK/模型/板端证据仍未补齐，不因文档进入审阅而宣称共享或兼容验证通过。
