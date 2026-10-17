# Implementation Plan: 修复 record 模块契约与并发缺陷

## 1. Ordered Checklist

- [x] **Step 1: CooldownManager 鲁棒性重构** (`internal/record/cooldown.go`, `cooldown_test.go`)
  - 修复 `Allow` 方法中的负时间差判定，仅在 `diff >= 0 && diff < cm.cooldown` 时返回 false。
  - 添加 `SetCooldown(d time.Duration)` 线程安全修改方法，避免全量重建实例。
  - 为 `CooldownManager` 添加专门单元测试（测试正向冷却、时间回退/乱序、并发更新）。

- [x] **Step 2: 序列化兜底与 Null 防御** (`internal/record/responses.go`, `service.go`)
  - 在 `responses.go` 的 `ToResponse` 中对 `candidates`、`attributes`、`extra` 增加 nil 兜底为 `[]` 和 `{}`。
  - 在 `service.go` 中，JSON 序列化前若对象为 nil，预设为 `"[]"` 或 `"{}"`。
  - 在 `service.go` 顶部增加 `res.Timestamp` 零值兜底（默认为 `time.Now().UTC()`）。

- [x] **Step 3: 优化告警标签识别** (`internal/record/service.go`, `service_test.go`)
  - 优化 `isAlarmLabel`，排除 `fire_hydrant`、`fire_extinguisher` 及正常佩戴安全帽 `helmet` 的误报。
  - 补充针对标签甄别规则的单元测试用例。

- [x] **Step 4: Store 与 Service 架构解耦** (`internal/record/store.go`, `service.go`, `module.go`)
  - 在 `Store` 中新增 `GetFaceImagePath(ctx context.Context, faceID string) (string, error)`。
  - 在 `Store` 的批量插入方法中增加单个 item nil 检查。
  - 在 `Service` 移除 `dbProvider` 字段，通过 `store.GetFaceImagePath` 解析人脸照片。
  - 更新 `NewService` 与 `NewRecordComponents` 签名，保持 Fx 装配一致。

- [x] **Step 5: Handler 消除吞错与测试补全** (`internal/record/handler.go`, `handler_test.go`)
  - 在 `ListRecognitions`、`ListAlarms`、`ListCaptures` 中处理 `c.ShouldBindQuery` 错误，调用 `httputil.HandleBindError`。
  - 在 `handler_test.go` 中真正向数据库插入记录，断言 `GET /:id` 正向 200 响应及 `candidates: []` 字段。

- [x] **Step 6: 全面验证与回归测试**
  - 运行 `go test -v -race ./internal/record/...`。
  - 运行 `make go-check`。

## 2. Validation Commands

```bash
# 验证 record 模块所有单测与竞态
go test -v -race ./internal/record/...

# 验证全局依赖与整体验证
make go-check
```
