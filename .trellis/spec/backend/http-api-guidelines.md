# HTTP API 与 Swagger 开发规范 (Gin)

> Gin REST API 开发、请求校验、Swaggo 2.0 文档及 SPA 路由隔离。

---

## 1. 路由组织与上下文解耦

- **前缀规范**：所有业务 API 统一挂载在 `/api/v1/`。各业务模块暴露 `RegisterRoutes(rg *gin.RouterGroup)`，由 `internal/app` 装配。
- **中间件挂载隔离**：业务 API 专用的 `AccessLog`、`CORS`、请求体限流等中间件必须统一挂载在 `/api/v1/` 路由组，严禁以 `router.Use()` 挂在全局根引擎，防止污染内嵌 SPA 静态资源与 Swagger 文档。详见 [HTTP 中间件开发与编排规范](./middleware-guidelines.md)。
- **Context 解耦**：`*gin.Context` 仅在 Handler 内部负责入参解析与响应，**严禁传入 Service/Store 或后台长时 goroutine**；透传上下文使用 `c.Request.Context()`。

---

## 2. 统一 API 响应信封与时间契约

所有 JSON 响应必须包含 `code`、`message`、`data` 三个字段；仅 HTTP 422 字段校验错误可额外包含 `details`。所有错误响应的 `data` 固定为 `null`。

- **单一真实源**：完整信封结构体定义、`AppError` 错误传播、HTTP 状态码映射与基于 `Accept-Language` 的多语言错误翻译契约，统一以 [错误处理与统一响应契约规范](./error-handling.md) 为准。
- **严禁裸写**：业务 Handler 必须通过 `httputil.Success(c, data)` 或 `httputil.WriteError(c, err)` 写回响应，严禁直接手写裸 `c.JSON(...)`。

### 时间字段格式与时区

- API 中表示绝对时刻的字段统一序列化为 UTC RFC3339Nano，并带 `Z`，例如 `2026-10-04T08:12:34.123Z`。Go 响应类型写出前应将 `time.Time` **逐个字段**调 `.UTC()` 规范化（见 §4「命名约定」）。

  **不要靠 "数据库里就是 UTC" 蒙混**：当前驱动恰好不输出偏移，是因为 DSN 没配 `_loc`，属于巧合而非契约。换个驱动、加个 DSN 参数、或给模型塞一个 `time.Now()` 本地位，响应就会静默变成 `+08:00`，而客户端按文档写的 `Z` 解析会直接报错。规范化必须在投影函数的每个字段上显式完成，并由测试断言 `Location() == UTC`。
- 纯日期使用 `YYYY-MM-DD`，不附加时区，也不转换为时间戳；持续时间使用带明确单位的数值字段。
- 数据库与 API 的详细存储/序列化约定见 [SQLite 与 GORM 数据库开发规范](./database-guidelines.md)；前端不得把本地化后的显示字符串回传为 API 时间值。

---

## 3. 统一分页与列表查询规约 (Pagination & List Queries)

为确保前后端在所有列表接口上的契约严格统一，禁止返回裸数组，所有分页接口遵循以下规约：

### 请求参数标准（Query Parameters）

- **`page`**：整数，页码，**从 1 开始**（默认 `1`）。若传入 `< 1`，后端强制归一化为 `1`；若 `> httputil.MaxPage`（100000），截断为该上限。**页码必须有上限**：缺失时 `(page-1)*pageSize` 会溢出成负数，而 SQLite 把负 `OFFSET` 当 `0`，越界请求会静默返回第一页。
- **`pageSize`**：整数，每页条数（默认 `20`，**硬上限 `100`**）。若传入 `<= 0`，归一化为 `20`；若 `> 100`，截断为 `100`。
- **兼容别名 `limit` / `offset`**：仍被接受，但语义与页码不同 —— **只给了 `offset`、没给 `page` 时，`offset` 原样使用，绝不反推页码再重算**。`limit=20&offset=30` 反推会得到第 2 页、`offset=20`，客户端拿到的就是第 21–40 条而不是它请求的第 31–50 条，且响应里看不出异常。`page` 与 `offset` 同时出现时以 `page` 为准。
- **唯一解析入口**：`(*httputil.PaginationQuery).Normalize()` 返回 `httputil.PageWindow{Page, PageSize, Offset}`，`Page` 与 `Offset` 恒自洽。**store / service 层不得再自行从 page/pageSize 推导 offset**（二段推导正是 legacy offset 被静默取整的根因），只允许对零值做防御（GORM 的 `Limit(0)` 表示不加 LIMIT，漏填 window 会把整表读进内存）。边界值统一引用 `httputil.DefaultPageSize` / `MaxPageSize` / `MaxPage`，不要在各领域抄第二份。

### 响应载荷四元组（Data Envelope）

所有分页接口的 `data` 必须返回固定四元组对象：

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "items": [ /* 数据项数组 */ ],
    "total": 128,
    "page": 1,
    "pageSize": 20
  }
}
```

- **空数组铁律**：无数据时 `items` **必须返回空数组 `[]`，严禁返回 `null`**。
- **响应类型字段 Null-safety 铁律**：当响应类型中包含反序列化 JSON 字段（如 `candidates`、`attributes`）时，严防 `json.Unmarshal([]byte("null"), &slice)` 将已初始化的空切片覆盖为 `nil`。在 `ToResponse()` 方法中反序列化后必须做显式兜底（`if slice == nil { slice = make(...) }`），确保输出到 API 时列表恒为 `[]`、字典恒为 `{}`，避免前端 Zod 校验因收到 `null` 抛错崩溃。
- **后端统一实现**：后端 Handler 统一使用 `httputil.PaginatedSuccess(c, items, total, window)` 输出，`window` 即解析出的 `PageWindow`；响应的 `page`/`pageSize`/`limit`/`offset` 必须与**实际查询的窗口**逐字段一致（不做二次换算）。
- **`limit` / `offset` 恒出现（无 `omitempty`）**：第一页 `offset=0` 时字段若消失，客户端无法区分「offset 为 0」与「服务端没返回 offset」。
- **前端统一契约**：前端 API 定义统一使用 `@/shared/api/client` 导出的 `createPaginatedSchema(itemSchema)` 校验，严禁直接使用裸 `z.array(itemSchema)` 解析分页接口。
- **不分页特例**：仅在数据项具备严格物理极小上限（如网卡列表 `interfaces` ≤ 8、固定枚举字典、明确 < 20 条的单例轻量配置）时允许不分页，且依然推荐返回 `{ items: [...] }` 保持结构一致。

---

### Scenario: 重复查询参数用于多值列表筛选

#### 1. Scope / Trigger

当列表筛选允许多选时，HTTP 查询参数必须保留多个相同 key 的值，且计数、排序和分页在后端同一查询中执行。该模式适用于审计日志 action 多选，不改变响应结构或数据库 schema。

#### 2. Signatures

- 兼容既有单值：`GET /api/v1/audit/logs?action=auth.login`
- 多值查询：`GET /api/v1/audit/logs?actions=auth.login&actions=camera.delete`
- Gin 请求字段：`Actions []string`，字段标签为 `form:"actions"`；前端 Axios 使用 `paramsSerializer: { indexes: null }` 生成重复 query key。

#### 3. Contracts

- 同一 `actions` 参数内按 OR 匹配；与 `status`、`startTime`、`endTime` 条件按 AND 组合。
- `actions` 非空时优先于兼容字段 `action`；仅在没有 `actions` 值时才应用单值 `action`。
- 后端先基于完整过滤条件计算 `total`，再按 `created_at DESC, id DESC` 排序并分页；响应仍使用 `{ items, total, page, pageSize }`。
- 后端 `pageSize` 最大为 100；浏览器列表 URL 对超过 100 的值回退到 API 默认值 20。

#### 4. Validation & Error Matrix

| 输入 | 行为 |
| --- | --- |
| 多个非空 `actions` 值 | 按 OR 过滤，其他筛选按 AND 组合 |
| 仅有旧版 `action` 值 | 保持精确单值过滤 |
| 同时传 `action` 和非空 `actions` | 采用 `actions`，避免把两种语义意外叠加 |
| 无效 RFC3339 时间 | API 返回 422；前端路由丢弃无效 URL 日期 |
| `pageSize` 大于 100 | 后端归一化为 100；前端 URL 解析回退到 20 |

#### 5. Good / Base / Bad Cases

- **Good**：`?actions=auth.login&actions=camera.delete&status=success&pageSize=1` 返回两个动作中符合状态的总数，并只返回当前页的一项。
- **Base**：旧客户端继续请求 `?action=auth.login`，得到原有精确匹配结果。
- **Bad**：先取一页再在 React 中过滤，会漏掉其他页的匹配项，并使 `total` 与真实匹配数不一致。

#### 6. Tests Required

- Handler：断言重复 `actions` 与 `status` 组合、分页后的 item 数和未分页 total，并保留单值 `action` 回归覆盖。
- Store：断言 `actions` OR、状态/时间 AND 与分页 offset 同时生效。
- 前端：断言 Axios 把 action 数组序列化成重复 query key，路由对 `pageSize=100` 接受、对 `101` 回退。

#### 7. Wrong vs Correct

**Wrong**：在当前页 `data` 上筛选多选 action，或用默认 bracket 数组序列化后假定后端收到重复 key。

**Correct**：

```typescript
apiClient.get('/audit/logs', {
  params: { actions: ['auth.login', 'camera.delete'] },
  paramsSerializer: { indexes: null },
})
```

## 4. 请求校验与响应类型边界

1. **强制类型隔离**：禁止在 HTTP 接口直接接收或返回 GORM Model，必须用独立的请求/响应类型，防止凭据泄露或级联循环。类型怎么取名见本节末「命名约定：请求 / 响应类型」。
2. **声明式 Binding 校验**：

   ```go
   type CreateCameraRequest struct {
       Name    string `json:"name" binding:"required,min=1,max=64"`
       RTSPUrl string `json:"rtspUrl" binding:"required,url"`
       FPS     int    `json:"fps" binding:"required,min=1,max=120"`
   }
   ```

3. **校验失败返回字段细节**：`c.ShouldBindJSON(&req)` 报错时将 validator 结果映射为请求 JSON 字段路径、稳定校验码和本地化消息，再以 HTTP 422 返回 `httputil.NewValidationError(message, details)`。不得把原始输入或 validator/底层错误字符串直接返回客户端。
4. **不可变字段必须显式拒绝，不得静默忽略**：对外业务键（如 `persons.personId`）等创建后不可修改的字段，必须**保留在 Update 请求类型中**，并对"改成不同值"的请求返回显式冲突错误（409 + 专属错误码，如 `PERSON_ID_IMMUTABLE`）。把字段从 Update 请求类型里删掉会让 `ShouldBindJSON` 静默丢弃它，接口返回 200 而值没变——集成方无法区分"改成功了"和"没生效"。
5. **部分更新（PATCH 语义）的三态约定**：Update 请求类型用指针字段区分"未提供"（`nil` → 不修改）与"提供了值"（→ 修改）。**严禁把"提供了空值"当成"未提供"**：`{"name": ""}` 必须返回 422 字段级校验失败，而不是 200 且值保持不变——后者与成功响应不可区分。若某字段确实允许被清空，就明确接受空值并写入空值，不要采用"空值即忽略"的隐式语义。
6. **查询参数里的破坏性开关必须严格解析，非法值 422**：`?replace=true` 这类在"追加"与"原子替换"之间切换语义的参数，**不得用 `c.Query(...) == "true"` 字面量比较**。`strconv.ParseBool` 能识别的 `TRUE`/`1` 会被静默当作 `false`，客户端以为在替换、服务端在追加，而 201 响应里除了一个 `replaced:false` 没有任何信号。用 `strconv.ParseBool` 解析，解析失败返回 `422` + 专属 code。
7. **体积错误要分层命名，不得把"太大"报成"没传"**：路径上限（如组级 11 MiB）、文件上限（如单图 10 MiB）与 JSON 体上限（1 MiB）是三个不同的限制。**解析阶段失败时必须先 `errors.As(err, &maxBytesErr)` 判 `*http.MaxBytesError`**：`c.FormFile` 在请求体被 `MaxBytesReader` 截断时返回的 err 与"没传文件"是同一个 `err` 位置，统一映射成 `FILE_REQUIRED` 会让客户端把"图太大"当成"忘传图"去改请求内容。固定映射：组级截断 → `413 PAYLOAD_TOO_LARGE`；端点自身体积检查 → `413 FILE_TOO_LARGE`；字段缺失 → `422 FILE_REQUIRED`。
8. **写权限后补读失败，不得让响应说谎**：写操作成功后为了丰富响应再读一次模型，失败时不能把成功报成失败（写已落库），但**也不能静默给一个猜的默认值**——那等于在成功响应里撒谎（典型：`isPrimary` 因补读失败而恒为 `false`）。优先让**写路径在事务内一次性返回**调用方需要的事实（如 `AddFaceWithLimit` 同时返回 `isPrimary`）；确实需要补读时，失败必须落 Warn 日志并回退到写路径已知的部分。

### 命名约定：请求 / 响应类型（强制）

`DTO` 在本项目是一个**概念**——"HTTP 层不直接收发 GORM Model"——**不是类型名后缀**。类型名按它在响应树里的位置取后缀：

| 位置 | 后缀 | 现有实例 |
| --- | --- | --- |
| 请求体（`c.ShouldBindJSON` 的目标） | `*Request` | `CreateCameraRequest`、`UpdateConfigRequest`、`UpsertDeviceRequest` |
| 顶层响应载荷（`httputil.Success` 的 `data`） | `*Response` | `CameraResponse`、`DeviceResponse`、`ConfigResponse`、`DeviceListResponse` |
| 嵌套在响应里的投影 / 状态块 | `*Info` | `StreamStateInfo`、`CameraStateInfo`、`InterfaceInfo`、`ConfigInfo` |
| GET 的遥测 / 状态载荷 | `*Status` | `SystemTimeStatus`、`StorageStatus` |

- **禁止** `XxxDTO` 形式的类型名。`DTO` 描述的是这一层的**职责**，后缀描述的是它在响应树里的**位置**；两套词并存会让同一层出现 `CameraResponse` 与 `CameraDTO` 两种风格。
- 转换函数以 `to<Type>` 命名（`toDeviceResponse`、`toConfigInfo`）；`internal/record` 的历史写法是模型方法 `func (r *Recognition) ToResponse() RecognitionResponse`，两种都接受，**但同一个包内保持一致**。
- 前端 Zod schema 名跟随 Go 响应名：`CameraResponse` ↔ `cameraResponseSchema`，列表载荷 `DeviceListResponse` ↔ `deviceListResponseSchema`。

**Why**：一旦出现 `CameraDTO`，`@Success ... {object} httputil.Response{data=...}` 注解、Swagger definition 名（`gb28181.ConfigDTO`）、前端 Zod schema 名会各自跑偏成三套词，review 时无法一眼判断"这个类型是不是对外契约"，也无法靠名字判断一个字段会不会进 API。

---

## 5. Swagger 2.0 注释与生成 (Swaggo)

在每个公开 Handler 上标注标准注释：

```go
// @Summary      获取单台摄像机详情
// @Tags         cameras
// @Produce      json
// @Param        id   path      string  true  "摄像机 ID"
// @Success      200  {object}  httputil.Response{data=CameraResponse}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id} [get]
```

- **生成命令**：从仓库根目录运行 `make api-docs`；该 target 固定 `swag` 版本并输出至 `internal/apidocs/`。
- **UI 挂载**：挂载在 `/swagger/*any`。

---

## 6. 前端 SPA 与 API 路由防击穿隔离

1. `/api/*` ➔ 业务 API。未命中（包含不支持的 HTTP method）必须返回 HTTP 404 JSON，遵循 `{ "code": "ROUTE_NOT_FOUND", "message": "Route not found", "data": null }`，**绝对禁止回退到前端 HTML**！
2. `/swagger/*` ➔ API 调试文档。
3. `/*` ➔ 前端 SPA 路由，未匹配静态资源一律回退至 `internal/webui` 内嵌的 `index.html`。

---

## 7. 裸字节数据面端点与 Swagger 白名单契约

### 7.1 二进制裸字节传输例外

- 统一响应信封（§2）**仅对 JSON 业务端点强制生效**。对于返回原始文件字节流的数据面端点（如 `internal/mediafile` 的 `/media/content/*path`），成功时允许直接返回裸 body（HTTP 200/206 Range 传输）。
- 该例外仅覆盖成功响应；除 416 Range Not Satisfiable（由标准库直接写纯文本）外，其余所有错误响应（401/404/422/503）仍必须返回统一 JSON 错误信封。
- 具体 Range 处理、416 文本、HEAD 显式注册与内核级 `os.Root` 防护详见单一真实源：👉 [媒体文件服务规范](./mediafile-guidelines.md)。

### 7.2 Swagger 生成的 `--dir` 显式白名单

`Makefile` 中 `make api-docs` 命令的 `--dir` 参数采用**显式目录白名单**。若新增了业务模块且包含 Swagger 注释端点，**必须同步在 `Makefile` 的 `api-docs` target 中追加该目录**；否则新端点不会被扫描，导致 Swagger 文档静默遗漏。

### 7.3 `@Router` 路径不带 `/api/v1` 前缀

`SwaggerInfo.BasePath` 已是 `/api/v1`，`@Router` 只写基准路径之后的相对段：

```go
// ✅ 生成 /api/v1/openapi/persons/{personId}
// @Router /openapi/persons/{personId} [post]

// ❌ 生成 /api/v1/api/v1/openapi/persons/{personId}，Swagger UI 里点不动
// @Router /api/v1/openapi/persons/{personId} [post]
```

生成物出现重复前缀时 Swagger UI 仍会渲染，只是 Try-it-out 全部 404 —— 不会报错，所以只能靠自动比对发现。

### 7.4 路径/方法与文档的自动对账

`@Router` 是**注释**，改路由签名时它不会跟着变。新增或调整端点后运行 `make api-docs`，并依赖仓内的契约测试守住三件事：

- **路由集对账**：`gin.Engine.Routes()` 里属于该模块的 (方法, 路径) 集合必须与生成 spec 的 `paths` 完全相等，多一个少一个都失败；
- **文档路径对账**：面向第三方的对接文档（如 `docs/openapi/person-face.md`）里出现的每个端点都必须真实挂载，且每个挂载端点都必须在文档里有条目；
- **响应字段对账**：文档承诺的 JSON 字段名必须与投影类型实际序列化结果一致（含「无数据时为 `[]` 而非 `null`」）。

参考实现：`internal/openapi/contract_test.go`。契约测试读的是 `swag.ReadDoc()` 的真实产物，不是手抄的第二份路径清单 —— 手抄的清单本身就是下一个漂移源。

---

## 8. 以图搜图（人员底库 1:N 检索）契约 (Image-Based Face Search Contract)

### 8.1 范围与触发 (Scope / Trigger)
跨层人员底库图片相似度检索（1:N 查询）与多维业务（抓拍、时空轨迹、档案）联动。该端点涉及大尺寸图片上传、临时内存人脸检测与特征提取（512 维向量）、向量点积相似度计算，以及按人聚合最高相似度。

### 8.2 签名与路由 (Signatures)
- **HTTP 方法与路径**：`POST /api/v1/persons/search-by-image`
- **路由分组**：`UploadRoutes`（要求 JWT 认证），限制最大请求体 `10 MiB`
- **Go Handler 签名**：`func (h *Handler) SearchByImage(c *gin.Context)`
- **Store 接口签名**：`ListSearchFaceItems(ctx context.Context) ([]SearchFaceItem, error)`
- **Service 接口签名**：`SearchByImage(ctx context.Context, imgBytes []byte, threshold float32, topK int) ([]ImageSearchMatchInfo, error)`

### 8.3 载荷契约 (Contracts)
- **请求（`multipart/form-data`）**：
  - `image`（file, 必填）：最大 10 MiB，单边最大 4096px，仅允许静态 JPEG / PNG / WebP（禁止 GIF/SVG/BMP）。
  - `threshold`（float32, 可选）：相似度下限阈值，默认 `0.6`，有效范围 `[0.0, 1.0]`。
  - `topK`（int, 可选）：返回人员匹配数量上限，默认 `10`，有效范围 `[1, 100]`。
- **响应载荷（`ImageSearchResponse`，置于 `httputil.Response.data`）**：
  ```json
  {
    "matches": [
      {
        "personId": "0194...",
        "personExternalId": "EMP-001",
        "personName": "张三",
        "matchedFaceId": "0194...",
        "similarity": 0.8854
      }
    ]
  }
  ```
- **内存生命周期与隐私约束**：
  - 查询图像字节流仅在单次请求内存中处理，**严禁持久化到磁盘或数据库**；
  - 提取的 512 维特征向量即用即弃，**严禁写入日志或审计明细**；
  - 审计动作 `person.search_by_image` 仅记录操作人、耗时、检索参数及命中人员数，禁止记录候选图像及人员完整列表。
- **单人多面容聚合规则**：若同一人员在底库拥有多张面容模板，检索结果中该人员仅出现一次，其分值为该人所有模板中的最高分，且 `matchedFaceId` 必须严格指向产出该最高分的面容 ID。

### 8.4 校验与错误矩阵 (Validation & Error Matrix)

| 条件 | HTTP 状态码 | 业务 Code | 说明 |
| --- | --- | --- | --- |
| 上传文件缺失或表单字段名不为 `image` | 422 | `VALIDATION_FAILED` | 表单字段缺失 |
| 文件大小超过 10 MiB | 413 | `FILE_TOO_LARGE` | 超过单文件上传体积上限 |
| 非 JPEG/PNG/WebP 格式或尺寸超过 4096px | 400 | `UNSUPPORTED_IMAGE_FORMAT` | 图片无法解码或尺寸超限 |
| 阈值或 Top-K 超出合法范围 | 422 | `VALIDATION_FAILED` | 参数校验不合法 |
| 图片未检测到有效人脸 | 400 | `NO_FACE_DETECTED` | 无法提取人脸特征向量 |
| 活动特征提取模型/算法未就绪或 IPC 异常 | 500 | `EXTRACTOR_ERROR` | 提取器崩溃或超时 |
| 底库数据库查询失败 | 500 | `DATABASE_ERROR` | SQLite 底库读取异常 |

### 8.5 典型案例 (Good / Base / Bad Cases)
- **Good**：传入 2MB 静态 JPEG，人脸位于中央，底库存在对应员工；返回 HTTP 200，`matches` 按 `similarity` 降序排列，单人仅保留一个最高相似度匹配项。
- **Base**：底库为空或所有人员相似度均低于 `threshold`；返回 HTTP 200，`matches: []`。
- **Bad**：传入损坏图片或风景照；返回 HTTP 400 `NO_FACE_DETECTED`。

### 8.6 必测断言点 (Tests Required)
1. 校验同人多模版聚合：底库单人含多个模版（得分 0.85 与 0.92），断言仅返回 1 条结果，`similarity` 为 0.92，且 `matchedFaceId` 等于对应模版 ID。
2. 过滤截断断言：低于 `threshold` 的匹配项被剔除，超出 `topK` 数量的尾部被丢弃。
3. 向量异常值保护：向量长度不足 512 或含 `NaN`/`Inf` 时静默丢弃或报错，不导致服务 panic。

### 8.7 错误 vs 正确对比 (Wrong vs Correct)
#### Wrong
```go
// ❌ 错误：在客户端响应中暴露服务器内部文件路径，且对每个面容模板单独返回一条记录导致单人重复
type WrongMatch struct {
    PersonName string  `json:"personName"`
    ImagePath  string  `json:"imagePath"` // 泄露磁盘私有路径
    Similarity float32 `json:"similarity"`
}
```
#### Correct
```go
// ✅ 正确：仅暴露人员外部/内部键与命中面容 ID；前端通过受保护端点按需获取预览；同人仅一条最高分
type ImageSearchMatchInfo struct {
    PersonID         string  `json:"personId"`
    PersonExternalID string  `json:"personExternalId"`
    PersonName       string  `json:"personName"`
    MatchedFaceID    string  `json:"matchedFaceId"`
    Similarity       float32 `json:"similarity"`
}
```

