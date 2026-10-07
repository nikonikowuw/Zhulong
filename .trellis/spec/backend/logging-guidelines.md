# Go 结构化日志规范 (Uber Zap)

> `go.uber.org/zap` 零分配结构化日志、强类型字段、等级划分与敏感凭据脱敏。

---

## 1. 核心原则

1. **强类型零分配 (Zero Allocation)**：
   - 热路径与核心业务统一使用 `*zap.Logger` 配合强类型字段（如 `zap.String()`, `zap.Int()`, `zap.Error()`），避免反射与堆内存分配开销。
   - 允许在非高频配置初始化阶段按需使用 `*zap.SugaredLogger`。
2. **绝对禁止敏感信息泄漏**：
   - 严禁打印密码、RTSP 流认证凭据、JWT 签名密钥或 API Token。
   - 严禁将原始视频帧、大块图像 Buffer 序列化至日志。
3. **分模块打标命名**：各模块使用子 Logger 区分业务域：`logger.Named("camera")` 或 `logger.With(zap.String("module", "camera"))`。
4. **停机主动刷新 (Sync)**：在 Fx `OnStop` 钩子中执行 `_ = logger.Sync()`，确保进程退出前缓冲区日志落盘。

---

## 2. 日志级别准则

| 级别 | 调用 | 适用场景 | 生产默认 |
| --- | --- | --- | --- |
| **DEBUG** | `logger.Debug` | 详细帧序列号、底层驱动临时状态、探测数据 | 关闭 (按需开启) |
| **INFO** | `logger.Info` | 关键生命周期（服务启动/停止、相机上线、录像落盘、配置更新） | **开启** |
| **WARN** | `logger.Warn` | 暂不影响主流程但需注意（短暂丢包重试、推理队列丢旧帧） | **开启** |
| **ERROR** | `logger.Error` | 功能性严重故障（硬件不可用、DB 失败、Pipeline 崩溃） | **开启** |

---

## 3. Logger 初始化与 Fx 集成模式

```go
// internal/app/logger.go
package app

import (
 "go.uber.org/zap"
 "go.uber.org/zap/zapcore"
)

func NewLogger(isDev bool) (*zap.Logger, error) {
 var cfg zap.Config
 if isDev {
  cfg = zap.NewDevelopmentConfig()
  cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
 } else {
  cfg = zap.NewProductionConfig()
  cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
 }
 logger, err := cfg.Build()
 if err != nil {
  return nil, err
 }
 zap.ReplaceGlobals(logger) // 替换全局，便于 httputil.Error 等无状态辅助函数直接获取 zap.L()
 return logger, nil
}
```

---

## 4. 业务安全与操作审计日志 (Audit Logs)

与面向开发者/运维排障的 Zap 运行日志不同，**操作与安全审计日志**面向管理员审计溯源，必须遵循以下规则：

1. **持久化与独立建模**：存储于 SQLite `audit_logs` 表，支持结构化查询（按动作、状态、时间范围过滤与分页）和管理员界面直观呈现。
2. **异步非阻塞管道**：业务关键路径调用 `audit.Service.Record(...)` 必须是非阻塞的（基于缓冲 Channel），缓冲区满时应降级记录警告并丢弃，绝不可阻塞主业务请求或导致写锁饥饿。
3. **容量保护与防爆盘 (FIFO Rolling Retention)**：默认设置严格上限（如 `5,000` 条），在批量落盘后触发滚动清理最旧记录，防止嵌入式存储空间被撑爆。
4. **凭据安全与脱敏**：
   - 严禁将明文密码、会话 Token 写入审计 `detail` 或 `error_msg`；
   - 包含流媒体凭证的 URL（如 RTSP）必须通过 `SanitizeURL` / `url.URL.Redacted()` 脱敏隐藏密码后再行记录。
5. **停机排空 (Graceful Drain)**：在系统关机生命周期中，必须在 HTTP 端口停止监听并排空流量后、SQLite 关闭连接前执行 `audit.Stop(ctx)`，将剩余缓冲区日志全部批量落盘。

