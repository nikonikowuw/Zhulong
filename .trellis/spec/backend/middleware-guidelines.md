# HTTP 中间件精简开发规范 (Gin)

---

## 1. 挂载隔离三原则

严禁在全局 `engine.Use()` 滥挂业务中间件，按路由严格隔离：

| 挂载层级 | 适用路径 | 允许挂载的中间件 | 核心禁忌 |
| --- | --- | --- | --- |
| **全局引擎** | `/*` 通用 | `Recovery`、`RequestID` | 严禁挂业务鉴权、AccessLog、CORS |
| **API 业务组** | `/api/v1/*` | `CORS`、`AccessLog`、`MaxBodyLimit`、`Auth` | 错误必须返统一 JSON，禁止 Fallback 静态页 |
| **静态与文档** | `/swagger/*`, `SPA` | 仅专用 Handler | 严禁挂日志、鉴权、限流，保障流式并发 |

---

## 2. 标准洋葱流水线与执行序

中间件注册顺序严格如下，逆序或乱序将导致 panic 漏捕获或追踪丢失：

```txt
Request ➔ [1. Recovery] ➔ [2. RequestID] ➔ [3. CORS] ➔ [4. AccessLog] ➔ [5. MaxBodyLimit] ➔ [Handler]
Response 桃 [1. Recovery 捕获] 桃 ------------------- 桃 [4. AccessLog 耗时统计] 桃 -------------------- ↵

```

* **Recovery 最外层**：确保接住下游所有 panic，防止边缘单进程崩溃。
* **RequestID 先于 AccessLog**：保证结构化日志能直接打印 `requestId`。
* **CORS 优先短路**：`OPTIONS` 预检请求必须在进入鉴权/耗时逻辑前立即 `204 Abort`。

---

## 3. 核心中间件设计红线

* **RequestID 链路追踪**：优先读 `X-Request-ID`，无则生成；必须双写回响应头与 `c.Request.Context()`。
* **Access Log (Zap)**：
* 热路径必须强类型字段输出（`zap.Duration`, `zap.Int`），禁止字符串拼接反射。
* 严禁打印密码、Token、RTSP 凭证与大体积 Body。
* 状态码分级：`≥500` Error，`≥400` Warn，其余 Info。

* **Recovery 防崩溃**：
* 遇到 `Broken Pipe` / `Connection Reset` 仅记 Debug/Warn，不打印全量堆栈。
* 对外一律脱敏返回统一 500 JSON：`{ "code": "INTERNAL_ERROR", "message": "...", "data": null }`。

* **MaxBodyLimit 防击穿**：边缘端非文件接口强制限制 Body 上限（如 1~2MB），超限直接截断并返 413，防 OOM。

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
