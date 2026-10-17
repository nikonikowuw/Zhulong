# Technical Design: 修复 record 模块契约与并发缺陷

## 1. 架构与边界调整

### 1.1 Store 与 Service 解耦
当前：
```txt
RecordComponents
  ├── Store (dbProvider)
  └── Service (Store, dbProvider) ──[Raw SQL]──> person_faces
```
调整后：
```txt
RecordComponents
  ├── Store (dbProvider) ───────────────> person_faces (GetFaceImagePath)
  └── Service (Store, logger, cooldown) ──> Store (纯依赖 Store)
```
- `Service` 结构体移除 `dbProvider database.DBProvider`。
- `NewService(store *Store, logger *zap.Logger)`。
- `Store` 暴露 `GetFaceImagePath(ctx context.Context, faceID string) (string, error)`。

### 1.2 序列化契约防御 (Null-safety)
Go 标准库 `json.Unmarshal([]byte("null"), &slice)` 会将目标切片赋为 `nil`。为保证 API 对前端输出严格满足 Zod 的非 null 要求：
1. **入库兜底**：在 `Service.processPersonStructure` 等消费逻辑中，`json.Marshal` 前若 slice/map 为 nil，显式置为 `[]byte("[]")` 或 `[]byte("{}")`。
2. **出库兜底**：在 `Recognition.ToResponse()`、`Alarm.ToResponse()`、`Capture.ToResponse()` 中，反序列化后做显式非空保障：
   ```go
   if candidates == nil {
       candidates = make([]aitypes.FaceCandidate, 0)
   }
   if attributes == nil {
       attributes = make(map[string]any)
   }
   ```

### 1.3 CooldownManager 鲁棒性设计
针对视频帧时间戳可能存在的非单调递增情况：
```go
func (cm *CooldownManager) Allow(key string, now time.Time) bool {
    cm.mu.Lock()
    defer cm.mu.Unlock()

    // 周期性淘汰（避免高频全量遍历）
    // ...

    lastTime, exists := cm.records[key]
    if exists {
        diff := now.Sub(lastTime)
        if diff >= 0 && diff < cm.cooldown {
            return false // 正向且未过冷却期，抑制
        }
    }

    cm.records[key] = now
    return true
}

func (cm *CooldownManager) SetCooldown(d time.Duration) {
    cm.mu.Lock()
    defer cm.mu.Unlock()
    if d > 0 {
        cm.cooldown = d
    }
}
```

### 1.4 告警与捕获标签甄别逻辑优化
针对 `isAlarmLabel(label string) bool`：
```go
var nonAlarmSubstrings = []string{"hydrant", "extinguisher"}

func isAlarmLabel(label string) bool {
    l := strings.ToLower(label)
    // 排除特定非告警物体，如 fire_hydrant
    for _, sub := range nonAlarmSubstrings {
        if strings.Contains(l, sub) {
            return false
        }
    }
    // 佩戴安全帽 helmet 属于合规状态，未佩戴 no_helmet / without_helmet 属于告警
    if l == "helmet" || l == "safety_helmet" || l == "wearing_helmet" {
        return false
    }
    return strings.Contains(l, "alarm") ||
        strings.Contains(l, "no_helmet") ||
        strings.Contains(l, "smoke") ||
        strings.Contains(l, "fire") ||
        strings.Contains(l, "flame") ||
        strings.Contains(l, "intrusion") ||
        strings.Contains(l, "fall") ||
        strings.Contains(l, "danger")
}
```

### 1.5 Handler 错误处理合规
在 `handler.go` 中，将：
```go
var q ListRecognitionsQuery
_ = c.ShouldBindQuery(&q)
```
改造为：
```go
var q ListRecognitionsQuery
if err := c.ShouldBindQuery(&q); err != nil {
    httputil.HandleBindError(c, err)
    return
}
```
保证非法参数立即被拦截并返回标准 400/422 信封。
