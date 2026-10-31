# 以图搜图（人员底库 1:N 检索与业务联动）— Technical Design

## 1. 架构与边界

```text
React 工作台
  ├─ 本地预览原图（不上传预览 URL，也不画人脸框）
  ├─ 阈值 / Top-K 控件
  └─ 结果卡片及业务跳转
       │ POST /api/v1/persons/search-by-image
       │ multipart: image, threshold?, topK?
       ▼
Go internal/person Handler（UploadRoutes、认证、请求/文件大小校验、审计）
       ▼
Person Service
  1. faceextract IPC：解码静态图片，提取一个 512D 归一化向量
  2. Store：读取所有可用 person_faces 特征
  3. 向量校验、点积、按 person_id 聚合最高分、阈值过滤、排序、Top-K
  4. 查询 Person 投影并返回 ImageSearchResponse
       │
       └─ UI 用 personId + matchedFaceId 调用受保护面容图片端点
```

`FaceFeatureExtractor.ExtractFaceFeature` 和 IPC 响应目前只返回向量。算法内部从检测到的人脸中选检测分数最高的一张；本期不扩展该接口，不提供用户在多张脸之间选择的交互，也不绘制查询图人脸框。UI 指引用户上传单张清晰人脸照片。

查询图片只在请求处理期间驻留内存。Handler 和 Service 不保存原图、查询向量或临时文件；审计仅记操作者、时间、结果状态和数量，不记录图片、向量或完整人员列表。

## 2. API 与数据契约

### 2.1 请求

- 路径：`POST /api/v1/persons/search-by-image`
- Content-Type：`multipart/form-data`
- `image`：必填，静态 JPEG / PNG / WebP，文件 part 最大 10 MiB；单边最大 4096 像素。动图 WebP 不接受。
- `threshold`：可选 Float，默认 `0.70`，闭区间 `[0,1]`。
- `topK`：可选整数，默认 `10`，闭区间 `[1,50]`。
- 文件 part 上限须在 Handler 中独立执行；UploadRoutes 的请求体上限只负责外层保护，不替代文件大小校验。

### 2.2 响应

响应使用统一 `httputil` 信封。`ImageSearchMatchInfo` 至少包含：

| 字段 | 语义 |
| --- | --- |
| `personId` | 内部 `persons.id`，用于应用内关联和导航 |
| `personExternalId` | 可空外部业务 ID，仅用于展示 |
| `personName` | 人员姓名 |
| `matchedFaceId` | 本次得分最高的底库面容 ID |
| `similarity` | 查询向量与该面容向量的原始余弦相似度 |

不返回 `image_path` 或服务器磁盘路径。前端通过现有 `/persons/{id}/faces/{face_id}/image` 端点读取 `matchedFaceId` 对应照片，确保展示的底库照片与用于取最大分的模板一致。主面容可能不是命中面容，不将二者混为一项。

### 2.3 错误映射

| 条件 | HTTP | code |
| --- | --- | --- |
| 缺少 image、multipart 无法解析、参数格式/范围错误 | 422 | `VALIDATION_FAILED` |
| 文件 part 超过 10 MiB | 413 | `FILE_TOO_LARGE` |
| 图片损坏、格式不支持或尺寸超限 | 400 | `UNSUPPORTED_IMAGE_FORMAT` |
| 未检测到可用人脸 | 400 | `NO_FACE_DETECTED` |
| 提取器未配置、IPC 超时/崩溃或协议错误 | 500 | `EXTRACTOR_ERROR` |

错误 envelope 的文案走现有后端本地化；内部错误原因只写服务端日志，不向客户端暴露。

### 2.4 特征与版本兼容

持久化向量为 512 个小端 Float32，共 2048 字节。检索前检查向量长度、提取结果维度及非有限值；无效底库向量跳过并记录计数，不允许 NaN/Inf 影响排序。

现有 `person_faces` 没有保存模型版本。上线前置门禁是确认所有现存底库特征与当前查询提取器来自同一兼容的 embedding 空间。若无法建立该事实，不能仅凭“同为 512 维”继续检索；应先另行设计模型身份持久化与底库重算，并将本功能上线阻塞到重算完成。任何算法包升级都要先验证向量兼容性；不兼容时重算底库后再开放检索。

## 3. 检索算法与性能

- 对每个有效面容计算 `dot(query, galleryFace)`，输入应为同一模型生成的 L2 归一化向量。
- 将计算结果限制在浮点误差边界 `[-1,1]`；不把负相似度伪装为 0。当前默认阈值为 0.70，因此负分不会成为结果。
- 以 `person_faces.person_id` 聚合，每个人只保留最高分对应的 `matchedFaceId`；之后按 `similarity >= threshold` 过滤，降序排序并截断至 `topK`。
- 初始容量目标为最多 10,000 张面容模板，使用本地精确 Flat 检索，不引入向量数据库。旧文档中的 400ns / 5ms 估算没有端到端测量依据，予以移除。
- 性能验证分别计时：Store 读取向量、纯排序计算、特征提取/IPC、完整 API；在确认的目标设备和运行时版本下，对 1k / 5k / 10k 模板报告 P50/P95 与峰值内存。目标硬件与 SLA 由产品/部署目标确认后再固化，不把纯点积耗时当成接口延迟。

## 4. 前端状态与导航

### 4.1 工作台

Feature 目录仍采用项目约定：

```text
web/src/features/image-search/
├── api/image-search-api.ts
├── api/image-search-api.test.ts
├── components/image-search-dropzone.tsx
├── components/image-search-controls.tsx
├── components/image-search-match-card.tsx
├── components/image-search-results.tsx
├── hooks/use-image-search.ts
├── locales/{en,zh-Hans,zh-Hant}.json
└── index.tsx
```

本地预览使用 object URL；替换、清除和卸载时 revoke。卡片显示 similarity（非 confidence），不使用固定 80% 颜色作为身份判断；每张卡使用 `matchedFaceId` 取对应登记照。工作台覆盖 idle、uploading、searching、empty、error、success 状态。

### 4.2 路由

- 新路由：`/_authenticated/image-search/`；侧边栏项注册在 General 分组。
- Records：`navigate({ to: '/records', search: { personId } })`，其中 `personId` 为内部行 ID。现有 Records 页消费该参数并查询识别记录。
- Spatial：跳转 `/spatial?targetType=person&personId=<internalId>`。为支持该深链接，Spatial route 增加 search 校验；`personId` 始终是内部行 ID。页面按该 ID 精确解析人员，再初始化人员选择器（选择器可显示其外部业务 ID）；轨迹请求使用解析出的人员键，后端支持内部 ID 与外部业务 ID。无显式地图参数时沿用空间页默认地图，无显式时间参数时沿用“今天”；地图和目标人员加载后自动提交一次查询。无地图时保留搜索目标并显示需配置地图状态。用户之后可改变时间并再次搜索。
- Persons：跳转 `/persons?personId=<internalId>`。Persons route 增加精确 ID 参数；用现有 `GET /persons/{id}` 获取目标人员并打开现有人脸/档案对话框，关闭时清除该参数。不能用姓名模糊搜索代替精确人员身份。
- i18n 注册 `imageSearch` namespace，并提供 `en`、`zh-Hans`、`zh-Hant` 三份资源。

## 5. 风险与回滚

- 当前方案不改 DB schema；前提是模型兼容门禁通过。如果无法确认存量底库向量兼容，需先另行补充模型版本追踪/底库重算设计，不能以“无需迁移”绕过风险。
- 功能回滚只移除搜索端点、页面、导航和路由；搜索不写新业务数据，不影响实时识别和既有记录。
- 人脸图像及向量属于敏感数据；实现必须遵守 REQ-B4 的临时处理、日志和审计边界。
