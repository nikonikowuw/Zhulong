# HTTP API 与 Swagger 开发规范 (Gin)

> Gin REST API 开发、请求校验、Swaggo 2.0 文档及 SPA 路由隔离。

---

## 1. 路由组织与上下文解耦

- **前缀规范**：所有业务 API 统一挂载在 `/api/v1/`。各业务模块暴露 `RegisterRoutes(rg *gin.RouterGroup)`，由 `internal/app` 装配。
- **Context 解耦**：`*gin.Context` 仅在 Handler 内部负责入参解析与响应，**严禁传入 Service/Store 或后台长时 goroutine**；透传上下文使用 `c.Request.Context()`。

---

## 2. 统一三字段响应体与后端 i18n 契约

所有 JSON 接口严格返回统一三字段信封 `{ code, message, data, details? }`：

- **`code`**：业务状态码。成功固定为 `"OK"`（或小写 `"ok"`）；错误码统一采用大写蛇形（如 `CAMERA_NOT_FOUND`、`VALIDATION_FAILED`）。
- **`message`（后端 i18n 翻译）**：由后端根据请求头 `Accept-Language`（`en` / `zh-Hans` / `zh-Hant`）自动翻译为用户首选语言。前端无需在客户端字典中映射错误码，直接通过 Toast 输出展示。
- **`data`**：业务数据载荷，失败时缺省。
- **`details`**：参数校验失败（HTTP 422）时的字段级细节。

```json
// 成功 (HTTP 200/201):
{ "code": "OK", "message": "success", "data": { "id": "cam_01", "name": "Gate-4K" } }

// 业务失败 (HTTP 404，由后端根据 Accept-Language: zh-Hans 翻译):
{ "code": "CAMERA_NOT_FOUND", "message": "指定的摄像机不存在" }

// 参数校验失败 (HTTP 422 附带 details):
{ "code": "VALIDATION_FAILED", "message": "请求参数校验失败", "details": [{ "field": "fps", "reason": "不能小于 1" }] }
```

---

## 3. 请求校验与 DTO 边界

1. **强制 DTO**：禁止在 HTTP 接口直接接收或返回 GORM Model，防止凭据泄露或级联循环。
2. **声明式 Binding 校验**：

   ```go
   type CreateCameraRequest struct {
       Name    string `json:"name" binding:"required,min=1,max=64"`
       RTSPUrl string `json:"rtspUrl" binding:"required,url"`
       FPS     int    `json:"fps" binding:"required,min=1,max=120"`
   }
   ```

3. **校验失败直接返回**：`c.ShouldBindJSON(&req)` 报错时直接输出 `httputil.NewError(http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Invalid input parameters", err)`。

---

## 4. Swagger 2.0 注释与生成 (Swaggo)

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

- **生成命令**：`swag init -g cmd/Zhulong/main.go -o internal/apidocs`
- **UI 挂载**：挂载在 `/swagger/*any`。

---

## 5. 前端 SPA 与 API 路由防击穿隔离

1. `/api/*` ➔ 业务 API。未命中必须返回 JSON 404 `{ "code": "ROUTE_NOT_FOUND" }`，**绝对禁止回退到前端 HTML**！
2. `/swagger/*` ➔ API 调试文档。
3. `/*` ➔ 前端 SPA 路由，未匹配静态资源一律回退至 `internal/webui` 内嵌的 `index.html`。
