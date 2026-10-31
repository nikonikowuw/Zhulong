# 以图搜图（人员底库 1:N 检索与业务联动）— Execution Plan

## 0. 实施前门禁

- [x] 确认当前正式 `face_recognition` 算法包/模型构建身份，并核实现存 `person_faces.feature_vector` 与查询提取器使用兼容的 embedding 空间。512 维和 L2 归一化不足以证明兼容；若无法确认或发现不兼容，暂停本任务实现/上线，先补模型版本追踪与底库重算方案。
- [x] 确认部署目标设备与算法包版本，记录 10,000 面容模板性能测试的设备、运行时和测试数据口径；未测量前不对完整 API 延迟承诺 5ms。

## 1. 后端 API 与检索服务（`internal/person`）

- [x] **1.1 Store 查询**：增加读取完整检索所需面容字段的 Store 能力，包含 face ID、内部 person ID 与 feature vector；不把服务器图片路径放入客户端响应。测试空底库、多个面容和无效长度向量。
- [x] **1.2 Service 检索**：实现图片特征提取、查询/底库向量维度及有限值校验、点积计算、按人聚合最高分并保留对应 `matchedFaceId`、阈值过滤、确定性排序和 Top-K。
- [x] 服务使用与现存底库兼容的活动提取器；无脸、坏图、提取器失败分别映射明确领域错误。多脸时遵循现有算法选最高检测分数人脸的行为，不提供人脸框。
- [x] **1.3 Handler 与路由**：新增 `ImageSearchRequest`、`ImageSearchResponse` / 匹配投影，注册 `POST /persons/search-by-image` 到 `UploadRoutes`；对文件 part 单独限制 10 MiB，校验参数、图片格式/尺寸并使用统一 envelope。
- [x] 接入审计动作；只记操作者、时间、结果状态和数量，不写图像字节、特征向量或完整候选列表。Handler 测试覆盖认证路由、缺失文件、错误参数、超限、错误状态码及成功响应。
- [x] 更新 API 文档和错误本地化；`FILE_TOO_LARGE` 为 413，输入校验 422，无脸/坏图 400，提取器故障 500。

## 2. 前端工作台（`web/src/features/image-search`）

- [x] **2.1 API**：新增请求方法与 Zod schema，按 multipart 发送 image / threshold / topK；测试信封解析、匹配字段和错误响应。
- [x] **2.2 交互与结果**：实现本地预览、拖放/选择、清除、阈值与 Top-K、上传/检索状态、空态和错误态。清理 object URL；不绘制人脸框；结果显示相似度而非置信度，并通过内部 person ID + matchedFaceId 的受保护端点加载实际命中的底库面容图。
- [x] **2.3 Records 联动**：按 `/records?personId=<internalId>` 导航；测试参数为内部人员行 ID 且目标记录视图应用该过滤。
- [x] **2.4 Spatial 联动**：为 Spatial route 增加并校验 `targetType` / `personId` 搜索参数；将内部人员 ID 精确解析为人员记录并初始化人员选择器，避免外部业务 ID 导致选择状态不匹配。使用默认地图与“今天”时间范围，在目标人员和地图就绪后自动查询一次；后端可接受内部 ID 或外部业务 ID。测试带外部业务 ID 的人员、有地图、无地图、无结果和手动改变时间范围。
- [x] **2.5 Persons 联动**：为 Persons route 增加精确 `personId` 参数，通过现有人员详情 API 获取对应行并打开现有档案/面容对话框；关闭时清理参数。测试同名人员不会误定位。
- [x] **2.6 国际化**：新增 `en`、`zh-Hans`、`zh-Hant` 资源并在 `web/src/lib/i18n.ts` 注册 `imageSearch` namespace。
- [x] **2.7 路由/导航**：添加 sidebar 项和 `/image-search` route；运行 TanStack Router 生成/校验流程，不手改生成文件。

## 3. 验证与性能

- [x] **3.1 后端单测**：向量边界、同人多面容取最高分及对应 face ID、排序/阈值/Top-K、请求校验和错误状态；通过 race 检查。
- [x] **3.2 前端测试**：API、工作台状态和三个业务跳转测试；真实无头浏览器环境下运行相关测试。
- [x] **3.3 构建与仓库检查**：运行前端测试与 build、API docs 检查及 `make check`；遵循 Makefile 的 native/Go 构建入口约束。
- [x] **3.4 性能测量**：在确认的目标设备和版本下，分别报告 1k / 5k / 10k 面容模板的向量读取、排序、特征提取/IPC、完整请求 P50/P95 与峰值内存。根据结果确认 SLA；不把算法估算当实测结果。

## 4. 回滚与风险

- 本方案默认无 DB migration，前提是第 0 节的模型兼容门禁通过。若不通过，先暂停并补充特征版本化/重算方案，不能把不兼容底库当作可搜索数据。
- 回滚时移除图片检索端点、工作台路由和导航即可；图片与查询向量不持久化，不需要清理新增业务数据。
- 前端预览只使用浏览器 object URL，替换、清除或离开页面时释放，不能把文件内容写入 query cache 或日志。
