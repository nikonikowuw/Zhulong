# 第三方平台人员与人脸 OpenAPI 下发接收

## Goal

为边缘宿主系统提供面向外部第三方业务平台（如门禁系统、考勤平台、智慧园区/校园中台）的标准 RESTful OpenAPI，支持外部平台通过安全的机器间（M2M）Bearer Token 认证，以单人增量/流式推送为主的形式下发并维护人员与人脸档案；在边缘端完成即时 AI 人脸特征建模、算法底库热同步与来源溯源标记，并在本地 Web UI 提供 API 访问凭据管理面板与人员来源徽标展示。

## 修订记录（评审后）

本次修订针对首版 PRD 的以下问题做了收敛，技术细节见同目录 `design.md`，实施顺序见 `implement.md`：

1. **资源锚点**：明确 OpenAPI 路径中的 `personId` 是外部业务键 `persons.person_id`，内部主键 `persons.id`（26 位 ULID）不出现在对外响应。
2. **响应模型**：不再复用内部 `PersonResponse` / `FaceResponse`，避免泄露 `ownerRowId`、`imagePath` 等存储细节。
3. **路由组**：OpenAPI 面档上传需要独立的 `/api/v1/openapi` 路由组，不能继承 1 MiB 的 `MaxBodyLimit()`。
4. **换头像原子性**：`?replace=true` 改为「先提取/落新文件，再单事务替换」，提取失败不得清空旧脸。
5. **错误码**：图片/人脸校验统一为 422，不再使用 400；补全 `INVALID_FILE_TYPE`、`FACE_LIMIT_EXCEEDED` 等既有码。
6. **Token 管理闭环**：补充 `GET/POST/DELETE /api/v1/api-tokens`、表结构、迁移、active-only 上限语义与软吊销。
7. **来源语义**：`source` / `source_name` 记录**首次录入来源**，创建后不可变；「谁最后修改」由审计日志承担。
8. **ID 类型**：内部人员/人脸 ID 是 ULID，不是 UUID；删除 PRD 中的 `UUID` 与 `fc_xxx` 示例。

## Background and Confirmed Facts

- **边缘算力与硬件约束**：人脸建模包含「图片解码 $\to$ 人脸检测与对齐 $\to$ 512 维 ArcFace 特征提取」流水线。`internal/faceextract.Client` 是 **concurrency-1** 的串行客户端（请求互斥 + 有界等待），单张在 RK3568 NPU 或 CPU 上的耗时取决于算法包与输入尺寸，PRD 不再把 100~300ms 当作既定事实，改为可测指标。大批量 Base64 JSON 传输会造成内存压力与网关超时；本任务采用**平台侧后台任务排队 + 边缘单人 `multipart/form-data` 推送**。
- **专网与隔离环境网络拓扑**：边缘设备通常部署在视频专网或受限局域网内，上级平台可以主动访问边缘设备的 IP/端口，但边缘设备通常缺乏向外反向访问外部 OSS/MinIO/静态资源服务器的路由权限或 DNS。因此图片传输必须以被动接收的二进制文件流为主。
- **现有人事与人脸数据模型**：
  - `persons.id`：内部代理主键，26 位 Crockford Base32 ULID（`internal/idutil`），不是 UUID；
  - `persons.person_id`：外部业务唯一键（工号/学号等），可空、唯一、一经绑定不可修改；
  - `persons.primary_face_id`：主照指针，指向 `person_faces.id`，`ON DELETE SET NULL`；
  - `person_faces`：与人员 1:N，`feature_vector` 为 512 个 float32 的 Little-Endian 字节（2048 字节）。
- **算法执行器底库**：通过 `GalleryNotifier`（IPC `BroadcastUpsert` / `BroadcastDelete`）热同步；冷启动走 `GetGallerySyncData` 全量分片。`BroadcastDelete` 支持「按 faceId 删单张」与「personId 为空 faceId 时删该人员全部」两种语义。
- **认证现状**：当前仅支持 Web 浏览器端 Cookie Session 鉴权（`internal/auth`），没有面向第三方系统的 M2M 凭据。
- **现有人员/人脸能力**：
  - 已支持单人建档、更新、删除、单张人脸录入、单张删除、设置主照、分页列表、按图检索；
  - 单张录入已有 10 MB 限制、JPEG/PNG/WebP 魔数嗅探、`ErrNoFaceDetected` / `ErrUnsupportedImageFormat` 哨兵错误；
  - **10 张/人上限目前只在 CSV/ZIP 批量导入路径生效**（`face_import.go` 的 `maxFacesPerPerson`），单张 `AddFace` 尚未执行该上限；本任务把上限提升为人员域统一规则；
  - 单张录入的图片校验错误走 `apperr.Invalid`，统一映射为 **422**，不是 400。
- **现有路由与中间件**：`/api/v1` 普通组为 1 MiB `MaxBodyLimit()`；大体积上传单独挂在 `/api/v1` 的 upload 组（500 MiB）。OpenAPI 面档上传必须另起独立组，否则真实照片会被 413 拒绝。
- **前端**：React 19 + TanStack Router + TanStack Query + Zod；设置页位于 `web/src/features/settings/`，人员页位于 `web/src/features/persons/`；复制必须使用 `web/src/lib/clipboard.ts`（明文 HTTP 下 `navigator.clipboard` 可能不存在）。

## Requirements

### R1. 机器间认证体系（API Tokens）

#### R1.1 凭据规格与高熵生成

- 令牌格式：`sk-` + 32 字节密码学安全随机数（64 位小写十六进制），总长 67 字符，例如 `sk-c65298078e42f97eb80d247b72a90e8d20a0a2cb61b2823c507861cbf16260c9`。
- 原始令牌只在创建响应中返回一次；服务端不持久化明文。

#### R1.2 零知识安全持久化

- 数据库只存 `token_hash = SHA256(raw_token)`（64 位小写十六进制），并建立唯一索引。
- 数据库同时存 `masked_key`（前 7 位 + `...` + 后 4 位，例如 `sk-c652...60c9`），仅用于前端核对。
- 原始令牌、`token_hash`、完整 `Authorization` 头不得进入日志、审计 `detail` / `target` 或错误响应。

#### R1.3 令牌生命周期与免重启热吊销

- Web UI 支持创建/查看/吊销 API Token；**最多 10 个生效（未吊销）令牌**，已吊销记录保留用于溯源，不计入上限。
- 备注名 `name`：必填，trim 后 1–48 个字符（保证 `api_token:` 前缀 + 名称不超过审计 `username` 的 64 字符契约），在生效令牌中唯一。
- 吊销采用软删除：写 `revoked_at`，记录保留；重复吊销幂等返回成功。
- 鉴权每次请求查库（`token_hash` 唯一索引 + `revoked_at IS NULL`），不做进程内缓存；吊销在下一次请求立即生效，无需重启，不影响视频流分析。

#### R1.4 活跃度审计

- 每次 Bearer 鉴权成功后刷新 `last_used_at`，但**不得每个请求同步写库**：同一 Token 最多每 60 秒落库一次，使用 `context.WithoutCancel` + 短超时，避免阻塞请求和 SQLite 单写者。
- 刷新失败只记日志，不影响请求结果。

#### R1.5 OpenAPI 鉴权中间件

- 只守护 `/api/v1/openapi/*` 路由组，不接管 Cookie Session；也不接受 Session Cookie 作为 M2M 凭据。
- 校验请求头 `Authorization: Bearer <token>`（scheme 大小写不敏感，重复/空值拒绝）；不支持 query 参数传递。
- Header 缺失、格式错误、令牌不存在或已吊销：统一返回 401 `UNAUTHORIZED`，并带 `WWW-Authenticate: Bearer`；不区分具体原因，避免探测。
- 数据库故障返回 500 `INTERNAL_ERROR`，不得伪装成 401。
- 鉴权成功后将 **Token ID、Token 名称** 通过私有类型化 key 注入 Gin 上下文，并提供导出 accessor；禁止裸字符串 `c.Set/c.Get`。
- 鉴权成功同时升级审计 Actor 为 `api_token:<token_name>`，IP 取 TCP 连接源地址。

#### R1.6 令牌管理 API（Web UI 后端）

- `GET /api/v1/api-tokens`：返回生效令牌列表 `{items: APITokenInfo[]}`，按创建时间倒序；字段：`id`、`name`、`maskedKey`、`createdAt`、`lastUsedAt`（可空，未使用为 `null`）。
- `POST /api/v1/api-tokens`：请求 `{name}`；返回 201 `{id, name, maskedKey, token, createdAt}`，`Cache-Control: no-store`。触发上限或名称冲突返回 409。
- `DELETE /api/v1/api-tokens/:id`：吊销指定令牌，返回 200 `{revoked: true}`；不存在返回 404 `API_TOKEN_NOT_FOUND`；已吊销幂等返回 200。
- 三个端点挂在 protected（Cookie Session）路由组，仅管理员可操作；创建/吊销写审计。
- 列表接口属于「严格极小上限」场景（≤10），不分页，但仍返回 `{items: []}` 结构。

#### R1.7 传输安全边界

- 当前设备以明文 HTTP 提供 SPA/API；Bearer Token 在同网段可被嗅探。本任务要求在交付文档和 UI 中明确「OpenAPI 仅允许部署在可信专网」，并保留后续 TLS/mTLS 的部署选项。
- Token 不得出现在 URL、Query、Referer 或前端持久化存储中。

### R2. 人员档案（Person）管理 OpenAPI

统一以外部工号 `personId` 作为资源定位锚点。

#### R2.1 人员创建与 Upsert 录入

- 端点：`POST /api/v1/openapi/persons`。
- 请求：`{ "personId": "EMP001", "name": "张三" }`。
- `personId`：必填，trim 后 1–64 字符，大小写敏感；禁止 `/`、`\`、`?`、`#`、`%` 与控制字符。
- `name`：必填，trim 后 1–64 字符。
- 幂等逻辑：
  - `personId` 不存在：创建人员，返回 **201**，`created=true`；
  - `personId` 已存在：仅更新姓名（值未变化则不写库），返回 **200**，`created=false`；
  - 并发 upsert 依赖 `person_id` 唯一索引 + `ON CONFLICT` 或冲突后重读，不得把唯一冲突暴露成 500。
- 来源：**创建时**写入 `source='API'`、`source_name=<当前 Token 名称>`；更新已存在人员时**不修改** `source` / `source_name`（来源表示首次录入方，最后修改方由审计记录）。
- 本地 Web UI 创建/CSV 导入的人员为 `source='LOCAL'`、`source_name=''`。

#### R2.2 人员修改

- 端点：`PUT /api/v1/openapi/persons/:personId`。
- 请求：`{ "name": "新姓名" }`，`name` 必填。
- `personId` 不存在返回 404 `PERSON_NOT_FOUND`；成功返回 200。
- 不接受 `personId` 字段变更；`person_id` 仍保持不可变。

#### R2.3 人员注销删除

- 端点：`DELETE /api/v1/openapi/persons/:personId`。
- 级联删除该人员名下所有人脸数据库记录与磁盘图片。
- 触发 `GalleryNotifier.BroadcastDelete`：逐脸删除 + 按 `personId` 删除兜底，确保算法底库不再识别该人员。
- 不存在返回 404 `PERSON_NOT_FOUND`；成功返回 200 `{deleted: true}`。

#### R2.4 人员详情查询

- 端点：`GET /api/v1/openapi/persons/:personId`。
- 返回仅含对外字段：`personId`、`name`、`source`、`sourceName`、`primaryFaceId`、`faceIds`、`createdAt`、`updatedAt`；不返回内部 `id`、`ownerRowId`、`imagePath`、特征向量。
- 不存在返回 404。

#### R2.5 路径编码

- `personId` 必须 URL 编码；服务端按解码后的值精确匹配，不做大小写折叠、不做 Unicode 归一化。

### R3. 人脸模态（Face）细粒度管理 OpenAPI

#### R3.1 人脸录入/追加

- 端点：`POST /api/v1/openapi/persons/:personId/faces`。
- 载荷：`multipart/form-data`，图片字段名 `image`；支持 JPEG、PNG、WebP；单文件 ≤ 10 MB。
- 可选 Query：`?replace=true`。
  - `false`（默认）：追加至该人员名下，受 10 张/人上限约束；
  - `true`：原子替换该人员全部历史人脸。**必须先把新图校验、解码、特征提取、落盘全部完成，再在单个数据库事务中删除旧脸并写入新脸**；任一步失败都不得删除旧脸。事务提交后再删旧文件并广播底库变更。
- 即时特征建模：调用 `FaceFeatureExtractor` 抽取 512 维特征，写入 `person_faces.feature_vector`，并调用 `GalleryNotifier.BroadcastUpsert`。
- 响应 201：`{ "faceId": "<ULID>", "isPrimary": true|false, "createdAt": "..." }`；`isPrimary` 以事务提交后的 `primary_face_id` 为准（首个脸自动成为主照）。
- 人员不存在返回 404 `PERSON_NOT_FOUND`；人员存在但未绑定 `person_id` 时按现有人事策略返回 409 `PERSON_ID_REQUIRED_FOR_ENROLLMENT`。

#### R3.2 图片校验与异常防御

统一使用 422 表示「客户端输入不可用」，不再使用 400：

| 条件 | HTTP | code |
| --- | ---: | --- |
| 缺 `image` 字段 | 422 | `VALIDATION_FAILED`（details: `image/REQUIRED`） |
| `replace` 不是布尔值 | 422 | `VALIDATION_FAILED`（details: `replace/INVALID_FORMAT`） |
| 扩展名或魔数不支持 | 422 | `INVALID_FILE_TYPE` |
| 图片损坏/无法解码 | 422 | `UNSUPPORTED_IMAGE_FORMAT` |
| 未检出有效人脸 | 422 | `NO_FACE_DETECTED` |
| 文件超过 10 MB | 413 | `FILE_TOO_LARGE` |
| 人员不存在 | 404 | `PERSON_NOT_FOUND` |
| 超过 10 张/人 | 409 | `FACE_LIMIT_EXCEEDED` |
| 提取器内部错误/超时 | 500 | `FEATURE_EXTRACT_FAILED` |
| 提取器过载（并发上限） | 429 | `TOO_MANY_REQUESTS` |

- 扩展名校验只作辅助：优先按内容魔数/`http.DetectContentType` 判断，避免第三方客户端上传无后缀文件被误拒。
- 现有 `SearchByImage` 对坏图返回 400 的历史行为不在本任务范围内；本任务只保证人员/人脸下发接口内部一致。

#### R3.3 人脸一键清空

- 端点：`DELETE /api/v1/openapi/persons/:personId/faces`。
- 删除该人员全部人脸记录与磁盘文件；`primary_face_id` 由外键 `ON DELETE SET NULL` 或显式更新清空。
- 广播 `BroadcastDelete("", personId)` 清空算法底库。
- 人员不存在 404；成功 200 `{deletedFaces: N}`；已无脸时幂等返回 0。

#### R3.4 单张人脸精准删除

- 端点：`DELETE /api/v1/openapi/persons/:personId/faces/:faceId`。
- 仅删除指定脸；若删除的是主照，自动将剩余人脸中最早的一张提升为主照，无剩余则置空（复用现有 `Store.DeleteFace` 语义）。
- 脸不存在或不属于该人员：统一 404 `FACE_NOT_FOUND`。
- 成功 200 `{deleted: true}`。

#### R3.5 设置主照

- 端点：`PUT /api/v1/openapi/persons/:personId/primary-face`。
- 请求：`{ "faceId": "<ULID>" }`。
- 人员不存在 404 `PERSON_NOT_FOUND`；脸不存在或不属于该人员 404 `FACE_NOT_FOUND`（对外不区分，避免泄露跨人员脸 ID）。
- 成功 200，返回人员详情投影。

#### R3.6 上限、幂等与重试语义

- 10 张/人是人员域统一规则：单张录入、追加、批量导入共用；超限 409 `FACE_LIMIT_EXCEEDED`。
- `POST .../faces` 天然非幂等；平台超时重试可能重复追加。PRD 要求平台在「换头像」场景使用 `?replace=true`，并在对接文档中说明该语义；`Idempotency-Key` 属于后续增强，不在本任务。
- `POST /persons` 与 `PUT /persons/:personId` 幂等；`DELETE` 幂等（重复删除返回 404 或成功均可，但文档必须固定一种；本任务固定为 404）。

#### R3.7 过载保护

- OpenAPI 路由组限制单 Token 同时最多 4 个面档请求、全局最多 8 个；超限返回 429 `TOO_MANY_REQUESTS` + `Retry-After: 1`。
- 该限制不阻断 Web UI 与批量导入；但人脸提取器是串行客户端，OpenAPI 请求会与导入任务竞争，文档需说明吞吐上限。

### R4. 数据溯源与操作审计

#### R4.1 持久化结构扩展

- `persons` 新增：
  - `source TEXT NOT NULL DEFAULT 'LOCAL'`，取值 `LOCAL` / `API`；
  - `source_name TEXT NOT NULL DEFAULT ''`，API 创建时存 Token 名称，本地为空。
- `source` 语义为**首次录入来源，创建后不可变**；对已存在人员的 API upsert 只更新姓名，不改变来源。
- GORM 模型必须显式赋值 `LOCAL`，不得依赖数据库默认值（GORM `Create` 会把零值 `''` 写进去）。
- 需要区分「当前由谁托管」时新增 `managed_by` 字段，不用 `source` 兼任。

#### R4.2 审计日志联动

- OpenAPI 的人员/人脸增删改接入既有 `audit` 中间件；读操作不审计。
- Actor 记录为 `api_token:<token_name>`；Token 名称上限 48 保证不超过审计 `username` 的 64 字符契约。
- 审计 `detail` 只写人员姓名、faceId、操作类型等，不写原始 Token、哈希、图片内容。
- 令牌创建/吊销本身也写审计（Actor 为当前管理员）。

#### R4.3 审计动作与文案

- 人员/人脸操作复用既有 `person.*` action，通过 Actor 区分来源；不重复造一套 `openapi.*`。
- 新增 `api_token.create` / `api_token.revoke`，同步：
  - Go 常量；
  - `web/src/features/audit/data/audit-actions.ts`；
  - `web/src/features/audit/locales/{zh-Hans,zh-Hant,en}.json`。

### R5. 前端交互与凭据管理（Web UI）

#### R5.1 API 访问凭据管理面板

- 位于「系统设置」，新增 `/settings/api-tokens` 子页与侧边栏入口。
- 列表展示：名称、脱敏 Key、创建时间、最近活跃时间（未使用显示「从未使用」）、[吊销] 按钮。
- 创建弹窗：备注名必填、1–48 字符；生成后高亮展示完整 Token 与「一键复制」；提示「关闭后无法再次查看完整内容」。
- 复制必须调用 `web/src/lib/clipboard.ts` 的 `copyToClipboard`，不得裸调 `navigator.clipboard`。
- 原始 Token 只保存在组件内存中，不写 `localStorage` / `sessionStorage` / URL；关闭弹窗即清除。
- 吊销前二次确认；失败展示后端本地化错误。

#### R5.2 人员列表来源呈现

- 人员表格与网格卡片对 `source='API'` 的人员显示 `<Badge variant="outline">{sourceName}</Badge>`。
- `source='LOCAL'` 不显示徽标。
- 为此 `personSchema` 增加 `source: 'LOCAL' | 'API'` 与 `sourceName: string`；后端 `PersonResponse` 增加对应字段（对既有前端是增量字段）。

#### R5.3 国际化

- 新增 UI 文案、字段标签、弹窗说明、空状态、错误提示完整支持 `en` / `zh-Hans` / `zh-Hant`。
- 后端新增错误码必须在各域 `locale.go` 注册三语文案；前端直接展示后端 `message`，不重复翻译。
- 新 i18n 命名空间需在 `web/src/lib/i18n.ts` 的三处资源中注册。

#### R5.4 前端实现约束

- 新增设置页路由后运行生成器更新 `web/src/routeTree.gen.ts`，不手改生成物。
- API 响应必须用 Zod 解析（`z.object` + 显式字段），不可断言类型。
- 组件测试跑真实无头浏览器；剪贴板测试需覆盖非安全上下文降级路径。
- `make api-docs` 的 `--dir` 白名单必须加入新的 OpenAPI / Token handler 包，否则 Swagger 静默缺项。

## API Contract Summary

| Method | Path | Success | 主要错误 |
| --- | --- | ---: | --- |
| POST | `/api/v1/openapi/persons` | 201 / 200 | 422, 409 |
| PUT | `/api/v1/openapi/persons/{personId}` | 200 | 404, 422 |
| DELETE | `/api/v1/openapi/persons/{personId}` | 200 | 404 |
| GET | `/api/v1/openapi/persons/{personId}` | 200 | 404 |
| POST | `/api/v1/openapi/persons/{personId}/faces` | 201 | 404, 409, 413, 422, 429, 500 |
| DELETE | `/api/v1/openapi/persons/{personId}/faces` | 200 | 404 |
| DELETE | `/api/v1/openapi/persons/{personId}/faces/{faceId}` | 200 | 404 |
| PUT | `/api/v1/openapi/persons/{personId}/primary-face` | 200 | 404 |
| GET | `/api/v1/api-tokens` | 200 | — |
| POST | `/api/v1/api-tokens` | 201 | 409, 422 |
| DELETE | `/api/v1/api-tokens/{id}` | 200 | 404 |

所有响应使用统一信封 `{code, message, data, details?}`；成功恒为 `code: "OK"`。

## Acceptance Criteria

- [ ] **AC1（Token 生命周期与存储安全）**：管理员可在 Web 端创建 `sk-<64hex>` 令牌；数据库仅存 SHA-256 哈希与脱敏 Key，无明文；最多 10 个生效令牌；名称 1–48 字符且生效令牌内唯一；吊销为软删除且立即生效。
- [ ] **AC2（OpenAPI 鉴权）**：有效 Bearer Token 能访问 `/api/v1/openapi/*` 并合并刷新 `last_used_at`；缺失/格式错误/伪造/已吊销统一 401 `UNAUTHORIZED`；数据库故障返回 500；Session Cookie 不能替代 Bearer。
- [ ] **AC3（人员 Upsert 与来源）**：新 `personId` 创建返回 201，`source='API'`、`source_name=<token.Name>`；已存在人员只更新姓名并返回 200，来源字段不变；并发 upsert 不返回 500。
- [ ] **AC4（单人流式录脸）**：合法人脸返回 201 与 `faceId`，数据库有 512 维特征，`BroadcastUpsert` 被调用；在 mock extractor 下端到端测试可稳定通过；板端 p95 作为实测指标记录，不作为 CI 门禁。
- [ ] **AC5（换头像原子性）**：`?replace=true` 在提取失败/写盘失败时保留全部旧脸；在成功时旧脸及旧文件全部清除，新脸成为主照，底库无孤儿特征。
- [ ] **AC6（坏图防御）**：损坏/不支持/无人脸分别返回 422 `UNSUPPORTED_IMAGE_FORMAT` / `INVALID_FILE_TYPE` / `NO_FACE_DETECTED`；超限 413；超 10 张 409。
- [ ] **AC7（删除语义）**：单张删除、清空人脸、注销人员均同步删除文件与算法底库；人员删除后算法停止识别；跨人员 faceId 返回 404。
- [ ] **AC8（Web UI）**：令牌列表、创建、复制、吊销在真实浏览器测试中通过；复制在非安全上下文走降级；人员表格/网格对 API 人员显示来源徽标。
- [ ] **AC9（安全与审计）**：原始 Token 不出现在数据库、日志、审计 detail/target；OpenAPI 写操作 Actor 为 `api_token:<name>`；令牌创建/吊销有审计记录；撤销后下一次请求即 401。
- [ ] **AC10（工程门禁）**：表驱动 Go 测试（含 `-race`）、真实浏览器前端测试、迁移 up/down 往返测试、Swagger 生成包含新端点；`make go-check`、`make frontend-check`、`make api-docs` 通过，最终 `make check` 通过。

## Out of Scope

- 边缘端反向调用外部 Webhook 回调（采用单次 HTTP 同步响应 + 平台侧任务队列管理）。
- 基于云端长连接（如 MQTT / WebSocket 反向 RPC）的双向指令通道。
- 细粒度的 API Token 接口级权限分配（当前所有生效 Token 具备统一的 OpenAPI 人员/人脸维护权限）。
- `Idempotency-Key` / 请求去重表。
- 把 `SearchByImage` 的坏图 400 历史行为改为 422（另立任务）。
- TLS/mTLS 终结（本任务只记录部署边界，要求可信专网）。
- 「当前由谁托管」的 `managed_by` 字段。

## Key Decisions

- **传输格式与粒度**：单人实时推送 + `multipart/form-data` 二进制流，避免 Base64 膨胀与大 JSON 内存压力；无外网依赖。
- **资源锚点**：对外只用 `personId`；内部 ULID 不暴露。Person / Face 拆为标准一级/二级资源。
- **换头像原子性**：先提取后事务替换，宁可失败也不丢旧脸。
- **凭据零知识存储**：256 位随机令牌 + SHA-256 哈希 + 脱敏快照；不需要 bcrypt（高熵秘密无需慢哈希）。
- **来源语义**：`source`/`source_name` 记录首次录入来源，创建后不可变；最后修改方由审计 Actor 表达，避免本地编辑把平台来源「洗白」。
- **错误语义**：客户端输入问题统一 422；资源不存在 404；冲突 409；过载 429；服务端故障 500。OpenAPI 与内部人员接口保持一致。
- **审计闭环**：Token 名称直接反哺人员 `source_name` 与审计 Actor；名称上限 48 以守住 `username` 64 字符契约。
- **路由隔离**：OpenAPI 独立路由组 + 独立 body limit + 独立 Token 鉴权，不复用 Session 保护组。
