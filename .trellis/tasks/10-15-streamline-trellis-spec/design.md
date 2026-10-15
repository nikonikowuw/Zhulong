# Technical Design: 精简与优化 .trellis/spec 工程规范

## 1. 单一真实源 (SSOT) 责任矩阵

为了彻底杜绝跨层/跨包重复描述，建立以下权威责任矩阵：

| 规范主题 | 唯一权威文件 (SSOT) | 关联消费文件 (仅链接，不复制正文) |
| :--- | :--- | :--- |
| **算法包物理结构与 `manifest.json` 元数据** | `backend/algorithm-guidelines.md` | `native/algo-plugin-contract.md` |
| **算法插件 C ABI、C++17 SDK 与 Arena 内存布局** | `native/algo-plugin-contract.md` | `backend/algorithm-guidelines.md` |
| **异构硬件加速后端 (RKNN / RGA / NPU)** | `native/hardware-backends.md` (新) | `native/algo-plugin-contract.md`, `architecture.md` |
| **HTTP 统一响应信封、`AppError` 与错误 i18n** | `backend/error-handling.md` | `backend/http-api-guidelines.md`, `backend/index.md` |
| **HTTP 路由组装配、分页标准与 Swagger 2.0** | `backend/http-api-guidelines.md` | `backend/error-handling.md` |
| **FrameBroker 共享内存 IPC 协议与对齐** | `native/algo-plugin-contract.md` | `backend/algorithm-guidelines.md`, `native/pipeline-guidelines.md` |
| **流摄取、探活与 CGO 桥接合同** | `native/ingestion-contract.md` | `backend/camera-guidelines.md`, `native/cgo-contract.md` |

---

## 2. 详细文件重构规划

### 2.1 Native 算法规范解耦拆分 (方案 A)
1. **新建 `native/hardware-backends.md`**：
   - 收敛 `IInferenceBackend` (RKNN) 与 `IVisionBackend` (RGA) 的编译期配置、依赖隔离与链接规范；
   - 完整保留 RK3568 实测硬核避坑结论：
     - RKNN 2.3.2 下 `RKNN_QUERY_MEM_SIZE` 导致 dup 上下文 SIGSEGV 的规避机制；
     - `rknn_dup_context` 权重共享与串行派生规则；
     - RGA 2D 光栅化防 OOB 越界挂死规则（目标缓冲全尺寸包装、`drect` 偏移、`imcheck_t` 预校验）；
     - 4 字节 stride 对齐与 2 像素 ROI 偏移对齐；
   - 剔除所有“任务 `10-13-*` 交付”字样，改为纯技术契约叙述。
2. **精简 `native/algo-plugin-contract.md`**：
   - 聚焦：C ABI 结构体、C++17 SDK、Contiguous Arena 打包、FrameBroker IPC 协议、底库热更新与推理结果上报（Egress）；
   - 引用 `backend/algorithm-guidelines.md` 获取 `manifest.json` 规则；
   - 引用 `native/hardware-backends.md` 获取硬件后端能力。
3. **更新导航索引**：
   - 在 `native/index.md` 增加 `[hardware-backends.md](./hardware-backends.md)` 索引条目。

### 2.2 后端规范去重与修剪
1. **`backend/algorithm-guidelines.md`**：
   - 保留：算法包物理拓扑（tar.gz/zip）、安全防护（Zip-Slip、解压炸弹）、`manifest.json` 权威字段表、Go 侧 Supervisor 进程监督模型与自愈；
   - 移除：重复的 C++ SDK 代码示范、纯 C ABI 签名声明、Runner 详细退出码；改为超链接指向 `native/algo-plugin-contract.md`。
2. **`backend/http-api-guidelines.md`**：
   - 精简第 2 节：删除重复的统一响应信封四元组字段详述与重复 JSON 块；仅保留关键指引并声明链接指向 `error-handling.md`；
   - 保持重点在：路由隔离、`/api/v1` 前缀、分页四元组契约、Swagger 2.0 规范。
3. **`backend/storage-guidelines.md`**：
   - 精简第 1.4 节：彻底删除“历史条目说明、过去为何废弃、旧错误码辩护”等长篇说教；
   - 直接陈述当前权威规则：“服务端绝对路径输入已彻底废弃，底库登记照统一使用 multipart/form-data 文件直传，入库仅保存相对文件名”。

### 2.3 Native Ingestion 合同中文化与精炼 (`native/ingestion-contract.md`)
- 保持技术语义 100% 准确（`options` 默认值、Go context 取消与原生超时解耦、`AVFormatContext` 线程亲和、流去重、优雅销毁）；
- 翻译为标准中文技术规范，剔除实验日记散文（如 “measured 4/15 failures under load” 提炼为明确的并发死锁规避规则）。

### 2.4 全局任务 ID 与历史痕迹清理
- 全局扫描并替换：
  - `architecture.md`: 移除任务代号括号备注，陈述客观实现。
  - `native/algo-plugin-contract.md` / `backend/storage-guidelines.md`: 移除任务编号和历史过程说明。

---

## 3. 链接与一致性保障机制

- 变更后通过 Python 脚本或自动化 grep 校验全仓 Markdown 链接：
  - 检查所有形如 `[xxx](./yyy.md)` 或 `[xxx](../yyy.md)` 的相对路径；
  - 验证每个目标文件确实存在；
  - 确保导航索引文件（`index.md`, `backend/index.md`, `native/index.md`, `frontend/index.md`）中的链接与文件名一致。
