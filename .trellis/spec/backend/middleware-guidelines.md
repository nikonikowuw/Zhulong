# HTTP 中间件开发与编排规范 (Gin)

> 中间件分层隔离、标准执行流水线、链路追踪、结构化 Access Log、安全脱敏与 Context 类型安全。

---

## 1. 核心分层隔离铁律 (Layered Isolation)

Zhulong 采用“单宿主二进制内嵌 WebUI 与 C++ 推理引擎”的交付架构。HTTP 服务同时承载业务 API、Swagger 文档与内嵌 SPA 静态资源。**绝对严禁将 API 业务中间件盲目以 `router.Use()` 挂载在全局引擎上！**

```txt
Gin Engine (Root)
├── [全局层] Recovery ➔ RequestID ➔ SecurityHeaders
│
├── /api/v1/ (业务 API 组)
│   └── [API 组] CORS ➔ AccessLog ➔ MaxBodyLimit ➔ (Auth/Timeout) ➔ 业务 Handler
│
├── /swagger/* (文档调试)
│   └── 仅 Swagger 专用 Handler (无 API 日志干扰，无鉴权阻断)
│
└── /* (内嵌 WebUI SPA)
    └── 静态资源托管与 NoRoute 回退 (无 API JSON 信封干扰，大文件免日志缓冲)
```

### 隔离原则

1. **全局引擎级（Global Engine）**：仅允许挂载无业务偏见、全路径通用的防爆底座：
   - `Recovery`：兜底 panic，拦截全站致命崩溃。
   - `RequestID`：全路径生成或透传分布式追踪标识。
2. **API 路由组级（API Group `/api/v1`）**：API 专用的中间件严禁污染静态资源：
   - `AccessLog`：结构化记录 API 响应时间与状态码（静态文件不刷屏）。
   - `CORS`：按需放行 API 跨域调用。
   - `MaxBodyLimit`：拦截畸形超大 Payload，防止边缘端内存击穿。
   - 统一错误返回：API 组内失败必须返回 `{ code, message, data: null }`，禁止静态资源回退。
3. **静态资源级（Static SPA / Swagger）**：
   - 严禁挂载 API 鉴权、速率限制或带有耗时缓冲的中间件，保障流式传输与静态资源并发性能。

---

## 2. 标准中间件流水线与执行时序 (Pipeline Order)

Gin 采用洋葱模型（Onion Model）。中间件挂载顺序决定了请求进入与响应离开的执行时序，必须严格遵守以下执行顺序：

```txt
Incoming Request
  │
  ▼
[1. Recovery]        (defer 捕获后置 panic)
  │
  ▼
[2. RequestID]       (生成/提取 X-Request-ID，写入 Header & Context)
  │
  ▼
[3. CORS]            (处理 OPTIONS 预检请求并快速短路中止)
  │
  ▼
[4. AccessLog]       (记录请求起始时间 start = time.Now())
  │
  ▼
[5. MaxBodyLimit]    (限制 request.Body 读取上限)
  │
  ▼
[6. 业务 Handler]    (解析参数、调用业务服务、返回 JSON)
  │
  ▼
[4. AccessLog 回程]  (计算 latency = time.Since(start)，Zap 强类型输出日志)
  │
  ▼
[1. Recovery 回程]   (无 panic 正常放行，有 panic 拦截写日志并输出 500 JSON)
  │
  ▼
Outgoing Response
```

### 顺序约束

- **Recovery 必须最外层**：确保下游任何中间件或 Handler 抛出 panic 都能被捕获，防止边缘进程崩溃。
- **RequestID 必须早于 AccessLog**：确保日志中间件打印 Access Log 时能够携带当前请求的 `requestId` 字段。
- **CORS 必须早于鉴权与日志**：HTTP `OPTIONS` 预检请求必须在进入耗时处理前立即返回 HTTP 204 并调用 `c.Abort()`。
- **AccessLog 必须包裹业务 Handler**：必须在 `c.Next()` 之后收集状态码、响应字节数与执行延迟。

---

## 3. 核心中间件契约与规范

### 3.1 Request ID 链路追踪中间件

- **请求头规范**：优先读取入站请求头 `X-Request-ID`；若无则自动生成（UUIDv4 或高效十六进制字符串）。
- **响应头回写**：必须在响应头中回写 `X-Request-ID`。
- **Context 注入**：
  - 注入 `*gin.Context`，便于 Handler 与后续中间件读取。
  - 将带有 Request ID 的 Go 标准 `context.Context` 注入到 `c.Request`，方便向下游数据库、异步长任务传递。

```go
// 概念实现
func RequestID() gin.HandlerFunc {
    return func(c *gin.Context) {
        reqID := c.GetHeader("X-Request-ID")
        if reqID == "" {
            reqID = generateID()
        }
        c.Header("X-Request-ID", reqID)
        SetRequestID(c, reqID)
        c.Next()
    }
}
```

### 3.2 结构化 Access Log 中间件 (Uber Zap)

- **强类型零分配**：热路径使用 `*zap.Logger`，禁止使用 `fmt.Sprintf` 反射拼接。
- **核心字段**：
  - `method`：HTTP 方法（`c.Request.Method`）。
  - `path`：原始请求路径（`c.Request.URL.Path`）。
  - `status`：最终 HTTP 状态码（`c.Writer.Status()`）。
  - `latency`：耗时（`time.Since(start)`，使用 `zap.Duration`）。
  - `clientIP`：客户端真实 IP（`c.ClientIP()`）。
  - `requestId`：关联的追踪 ID。
- **分级策略**：
  - `status >= 500` ➔ `logger.Error`
  - `status >= 400` ➔ `logger.Warn`
  - 正常 `2xx/3xx` ➔ `logger.Info`
- **脱敏与流媒体保护**：
  - 严禁打印 Authorization 令牌、密码、RTSP URL 凭证等敏感信息。
  - 严禁将大体积 Body 写入日志。
  - 对于 WebSocket、SSE 或未来音视频裸流接口，禁止缓冲 Body，日志仅记录握手建立与断开。

### 3.3 统一 Recovery 防崩溃中间件

- 拦截所有未捕获的 panic。
- 判断是否为网络管道断开（`Broken Pipe` / `Connection Reset by Peer`）；如果是，记录轻量日志并直接中止，不打印冗长堆栈。
- 常规 panic 必须使用 `logger.Error("panic recovered", zap.Any("panic", err), zap.Stack("stack"))` 详细记录（仅服务端可见）。
- 对客户端返回统一脱敏响应：HTTP 500，`{ "code": "INTERNAL_ERROR", "message": "...", "data": null }`，严禁暴露堆栈或内部指针信息。

### 3.4 请求体防击穿中间件 (Max Body Limit)

- 边缘设备内存极其宝贵，必须限制非文件上传 API 的 Request Body 上限（如常规 JSON 限制 1MB ~ 2MB）。
- 超出上限时终止并返回 HTTP 413 Payload Too Large，避免恶意或故障请求引发宿主 OOM。

---

## 4. Context 存取类型安全规范 (Type-Safe Accessors)

严禁在 Handler 或中间件中直接硬编码魔术字符串读写 `gin.Context`（如 `c.Set("userId", 123)` / `c.Get("userId")`），否则极易导致拼写错误或类型断言 panic。

### 强类型封装准则

1. 上下文 Key 必须定义为包级私有常量。
2. 必须暴露成对的导出的 Setter 与 Getter 函数。
3. Getter 函数必须处理不存在或类型断言失败的情况，提供安全默认值或明确返回布尔标记。

```go
package middleware

import "github.com/gin-gonic/gin"

const requestIDKey = "zhulong.request_id"

// SetRequestID 存储追踪 ID 到 Gin 上下文
func SetRequestID(c *gin.Context, id string) {
    c.Set(requestIDKey, id)
}

// GetRequestID 安全获取追踪 ID，不存在时返回空字符串
func GetRequestID(c *gin.Context) string {
    if val, ok := c.Get(requestIDKey); ok {
        if id, ok := val.(string); ok {
            return id
        }
    }
    return ""
}
```

---

## 5. Uber Fx 依赖注入集成与无副作用构造

1. **构造函数无副作用**：中间件构造函数只接收依赖对象（如 `*zap.Logger`、配置对象），严禁在构造时启动后台轮询或占用系统资源。
2. **闭包工厂模式**：需要依赖项的中间件，统一通过工厂函数返回 `gin.HandlerFunc`：

```go
package middleware

import (
    "github.com/gin-gonic/gin"
    "go.uber.org/zap"
)

// NewAccessLogger 构造依赖注入的 Access Log 中间件
func NewAccessLogger(logger *zap.Logger) gin.HandlerFunc {
    log := logger.Named("http")
    return func(c *gin.Context) {
        // 中间件逻辑...
    }
}
```

3. **装配收敛**：中间件提供者及挂载点统一由 `internal/app` 装配至 Gin Engine 或 RouterGroup，业务包不直接创建全局中间件实例。

---

## 6. 流媒体与异构长连接注意事项

- **SSE / 推理事件流**：长连接生命周期由客户端主动关闭或服务停机信号决定；中间件**不得设置固定短期 Read/Write 超时**，也不得在中间层做全量缓冲。
- **WebSocket 握手**：中间件在 `c.Next()` 之后不得假设 Response Header 尚未发送，因为协议升级会直接接管底层 TCP 连接。
- **CGO 异步推理交互**：中间件内严禁直接调用耗时较长的 CGO 原生阻塞接口；长耗时计算必须交由后台 worker 或专门的异步调度协程。
