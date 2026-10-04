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
