# HTTP 中间件精简开发规范 (Gin)

---

## 1. 挂载隔离三原则（四层级）

严禁在全局 `engine.Use()` 滥挂业务中间件，按路由严格隔离：

| 挂载层级 | 适用路径 | 允许挂载的中间件 | 核心禁忌 |
| --- | --- | --- | --- |
| **全局引擎** | `/*` 通用 | `Recovery`、`RequestID` | 严禁挂业务鉴权、AccessLog、CORS |
| **API 业务组** | `/api/v1/*` | `CORS`、`AccessLog`、`MaxBodyLimit`、`Auth` | 错误必须返统一 JSON，禁止 Fallback 静态页 |
| **上传业务组** | `/api/v1/*`（独立 `Group`） | `AccessLog`、**放宽的** `MaxBodyLimitWithLimitAndCode`、`Auth` | 严禁复用默认 `MaxBodyLimit()`（1 MiB 会直接毙掉合法上传） |
| **静态与文档** | `/swagger/*`, `SPA` | 仅专用 Handler | 严禁挂日志、鉴权、限流，保障流式并发 |

> ⚠️ **上传端点必须另起一个 Group，而不是挂在 API 业务组下面。** Gin 中间件在 `Group()` 调用时即被求值，无法在事后「放宽」已挂载的 `MaxBodyLimit()`；因此在受保护组下声明的上传路由会**永久继承 1 MiB 的 `MaxAPIRequestBodyBytes`**，体积合法的上传被 413 直接拒绝。
>
> 正确做法是在 `internal/app/router.go` 里单独建一个同前缀的组，只挂上传端点，并对其单独施加放宽的限额（当前 `algorithm.MaxUploadRequestBodySize` = 500 MiB 归档 + 1 MiB multipart 框架开销）、独立的 `AccessLog` 与 `Audit`（该组不会继承 API 业务组的中间件）、以及 `RequireAuth`：
>
> ```go
> upload := router.Group("/api/v1")
> upload.Use(httpmiddleware.AccessLog(p.Logger),
>     httpmiddleware.MaxBodyLimitWithLimitAndCode(algorithm.MaxUploadRequestBodySize, "FILE_TOO_LARGE"))
> if p.AuditService != nil { upload.Use(audithttp.Middleware(p.AuditService)) }
> if p.AuthService != nil { upload.Use(auth.RequireAuth(p.AuthService)) }
> for _, r := range p.UploadRoutes { r.RegisterRoutes(upload) }
> ```
>
> 回归护栏：`internal/app/upload_router_test.go` 会走**真实 router** 上传一份 > 1 MiB 的归档并断言 `201 Created` 而非 413；只测 handler 的单元测试永远发现不了这个坑。
>
> 当前挂载在 `upload_routes` 组的端点：`POST /api/v1/persons/{id}/faces`（10 MiB 登记照）、算法包安装归档。

---

## 2. 标准洋葱流水线与执行序

中间件注册顺序严格如下，逆序或乱序将导致 panic 漏捕获或追踪丢失：

```txt
Request ➔ [1. Recovery] ➔ [2. RequestID] ➔ [3. CORS] ➔ [4. AccessLog] ➔ [5. MaxBodyLimit] ➔ [Handler]
Response 桃 [1. Recovery 捕获] 桃 ------------------- 桃 [4. AccessLog 耗时统计] 桃 -------------------- ↵

```

* **Recovery 最外层**：确保接住下游所有 panic，防止边缘单进程崩溃。
  * ⚠️ handler panic 时，外层中间件 `c.Next()` 之后的代码**不会执行**（gin 的 handler 索引在 panic 时不推进）；`defer` 中读取 `c.Writer.Status()` 只会得到默认 `200`。需要感知 panic 的下游能力（如审计）必须通过 `Recovery(logger, observers...)` 的 `PanicObserver` 回调，而不是自行在 `c.Next()` 后收口。
  * `PanicObserver` 接口定义在 `internal/httpmiddleware`（不导入业务包），具体实现由 `internal/app` 注入，保持单向依赖。
* **RequestID 先于 AccessLog**：保证结构化日志能直接打印 `requestId`。
* **CORS 优先短路**：`OPTIONS` 预检请求必须在进入鉴权/耗时逻辑前立即 `204 Abort`。

---

## 3. 核心中间件设计红线

* **RequestID 链路追踪**：优先读 `X-Request-ID`，无则生成；必须双写回响应头与 `c.Request.Context()`。
* **Access Log (Zap)**:
* 热路径必须强类型字段输出；普通请求记录 `latency_ms`，SSE/WebSocket 分别记录 `stream.started` / `stream.closed`，成功连接使用 `session_ms`，失败握手使用 `established=false` 与 `latency_ms`。
* access event 固定为 `Info`，不论状态码；5xx 的唯一 `Error` 归属 `httputil.WriteError`。必须记录 route template，不记录原始 URL/path、query、凭据或大体积 body。
* **Recovery 防崩溃**：
* 对外一律脱敏返回统一 500 JSON：`{ "code": "INTERNAL_ERROR", "message": "...", "data": null }`。
* panic 内部信息与堆栈由唯一的 `WriteError` Error event 承载；Recovery 不再额外输出第二条 Error。`PanicObserver` 仍在响应写完后通知审计层。
* Broken Pipe / Connection Reset 作为客户端断开处理时，不额外打印完整堆栈。

* **MaxBodyLimit 防击穿**：边缘端非文件接口强制限制 Body 上限（如 1~2MB），超限直接截断并返 413，防 OOM。
  * `ContentLength` 预检在路由 Handler 之前拒绝请求；无 `Content-Length` 的 chunked 请求会穿透预检，改由 `http.MaxBytesReader` 在绑定阶段拒绝。两者对审计的含义不同：前者无审计意图，后者属于「已声明意图的绑定失败」，需要留痕。
  * **该中间件按组求值，无法在组内「放宽」**：需要更大限额的端点必须挂到独立的上传组，详见 [§1 挂载隔离三原则（四层级）](#1-挂载隔离三原则四层级) 的上传业务组行。

* **413 错误码归属**：`MaxBodyLimitWithLimitAndCode` 的 `errorCode` 在**整组**生效。上传组统一用 `FILE_TOO_LARGE`，而 person 域 `Service.AddFace` 对「文件本身超过 10 MiB」也返回 `FILE_TOO_LARGE`——两者虽然同为 413 + 同码，但语义分层不同（中间件层 = 请求体超限；领域层 = 文件超限）。前端若要区分，只能依据 `details` 或文案，**不要**依赖 `code` 去判断是哪一层拒绝的。

---

## 4. 上下文与代码规范

### Context 强类型存取 (拒绝魔术字符串)

```go
const reqIDKey = "zhulong.request_id"

func SetRequestID(c *gin.Context, id string) { c.Set(reqIDKey, id) }

func GetRequestID(c *gin.Context) string {
    if v, ok := c.Get(reqIDKey); ok {
        if id, ok := v.(string); ok { return id }
    }
    return ""
}

```

### Uber Fx 工厂构造

* 构造函数必须无副作用（禁止在 New 内起协程或占用外设）。
* 统一通过闭包工厂接收注入参数：

```go
func NewAccessLogger(log *zap.Logger) gin.HandlerFunc {
    logger := log.Named("http")
    return func(c *gin.Context) { /* c.Next() + 计时打点 */ }
}
```

---

## 5. 异构长连接与 CGO 保护

* **SSE / 裸流**：禁止设置短期读写超时，禁止在中间件全量缓冲响应体。
* **WebSocket**：握手接管 TCP 连接后，后续中间件不得修改 Response Header。
* **CGO 引擎**：严禁在中间件热路径内同步调用 CGO 阻塞方法，长任务必须异步交由后台 Worker。
