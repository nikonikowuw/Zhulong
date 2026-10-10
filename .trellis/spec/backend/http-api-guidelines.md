# HTTP API 与 Swagger 开发规范 (Gin)

> Gin REST API 开发、请求校验、Swaggo 2.0 文档及 SPA 路由隔离。

---

## 1. 路由组织与上下文解耦

- **前缀规范**：所有业务 API 统一挂载在 `/api/v1/`。各业务模块暴露 `RegisterRoutes(rg *gin.RouterGroup)`，由 `internal/app` 装配。
- **中间件挂载隔离**：业务 API 专用的 `AccessLog`、`CORS`、请求体限流等中间件必须统一挂载在 `/api/v1/` 路由组，严禁以 `router.Use()` 挂在全局根引擎，防止污染内嵌 SPA 静态资源与 Swagger 文档。详见 [HTTP 中间件开发与编排规范](./middleware-guidelines.md)。
- **Context 解耦**：`*gin.Context` 仅在 Handler 内部负责入参解析与响应，**严禁传入 Service/Store 或后台长时 goroutine**；透传上下文使用 `c.Request.Context()`。

---

## 2. 统一 API 响应信封与后端 i18n 契约

所有 JSON 响应必须包含 `code`、`message`、`data` 三个字段；仅 HTTP 422 字段校验错误可额外包含 `details`。所有错误响应的 `data` 固定为 `null`。

- **`code`**：稳定的机器可读业务状态码。成功固定为 `"OK"`；错误码采用大写蛇形（如 `CAMERA_NOT_FOUND`、`VALIDATION_FAILED`）。
- **`message`**：后端根据请求头 `Accept-Language`（`en` / `zh-Hans` / `zh-Hant`）本地化。前端可直接展示，不需再次翻译业务错误码。
- **`data`**：成功时为业务载荷；错误时必须为 `null`，不得省略。
- **`details`**：仅用于 HTTP 422 字段校验错误，可选数组。每项包含请求 JSON 字段路径、稳定字段错误码和按 `Accept-Language` 本地化的消息。不得包含被拒绝的原始输入或内部错误。

```json
// 成功 (HTTP 200/201)
{ "code": "OK", "message": "success", "data": { "id": "cam_01", "name": "Gate-4K" } }

// 一般错误 (HTTP 404)
{ "code": "CAMERA_NOT_FOUND", "message": "指定的摄像机不存在", "data": null }

// 字段校验错误 (HTTP 422)
{
  "code": "VALIDATION_FAILED",
  "message": "请求参数校验失败",
  "data": null,
  "details": [
    { "field": "fps", "code": "MIN_VALUE", "message": "帧率不能小于 1" }
  ]
}
```

---

### 时间字段格式与时区

- API 中表示绝对时刻的字段统一序列化为 UTC RFC3339Nano，并带 `Z`，例如 `2026-10-04T08:12:34.123Z`。Go 响应 DTO 写出前应将 `time.Time` 规范化为 UTC。
- 纯日期使用 `YYYY-MM-DD`，不附加时区，也不转换为时间戳；持续时间使用带明确单位的数值字段。
- 数据库与 API 的详细存储/序列化约定见 [SQLite 与 GORM 数据库开发规范](./database-guidelines.md)；前端不得把本地化后的显示字符串回传为 API 时间值。

---

## 3. 统一分页与列表查询规约 (Pagination & List Queries)

为确保前后端在所有列表接口上的契约严格统一，禁止返回裸数组，所有分页接口遵循以下规约：

### 请求参数标准（Query Parameters）
- **`page`**：整数，页码，**从 1 开始**（默认 `1`）。若传入 `< 1`，后端强制归一化为 `1`。
- **`pageSize`**：整数，每页条数（默认 `20`，**硬上限 `100`**）。若传入 `<= 0`，归一化为 `20`；若 `> 100`，截断为 `100`。
- **兼容别名支持**：后端通过 `httputil.PaginationQuery` 统一解析，若请求提供了 `limit` / `offset`，自动换算为对应的 `page` 与 `pageSize`。

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
- **后端统一实现**：后端 Handler 统一使用 `httputil.PaginatedSuccess(c, items, total, page, pageSize)` 输出；
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

## 4. 请求校验与 DTO 边界

1. **强制 DTO**：禁止在 HTTP 接口直接接收或返回 GORM Model，防止凭据泄露或级联循环。
2. **声明式 Binding 校验**：

   ```go
   type CreateCameraRequest struct {
       Name    string `json:"name" binding:"required,min=1,max=64"`
       RTSPUrl string `json:"rtspUrl" binding:"required,url"`
       FPS     int    `json:"fps" binding:"required,min=1,max=120"`
   }
   ```

3. **校验失败返回字段细节**：`c.ShouldBindJSON(&req)` 报错时将 validator 结果映射为请求 JSON 字段路径、稳定校验码和本地化消息，再以 HTTP 422 返回 `httputil.NewValidationError(message, details)`。不得把原始输入或 validator/底层错误字符串直接返回客户端。

---

## 5. Swagger 2.0 注释与生成 (Swaggo)

在每个公开 Handler 上标注标准注释：

```go
// @Summary      获取单台摄像机详情
// @Tags         cameras
// @Produce      json
// @Param        id   path      string  true  "摄像机 ID"
// @Success      200  {object}  httputil.Response{data=CameraDTO}
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
