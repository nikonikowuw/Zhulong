# 空间画布与轨迹分析规范

> `internal/spatial` 的底图存储生命周期、格式与安全沙盒约束、相机坐标归一化及轨迹驻留聚合契约。

---

## 1. 生命周期与路由分流

- **底图目录在 `Start` 创建**：`spatial.Start` 负责 `os.MkdirAll(<data-dir>/maps)`；`NewSpatialComponents` 构造函数严禁触碰文件系统。若数据目录不可写，`Start` 返回错误并进入宿主降级状态（控制台仍可用，底图读写返回 5xx）。
- **上传与普通路由分离**：
  - `POST /api/v1/spatial/maps`（多部分文件上传）必须挂入 `upload_routes` 值组（通过 `handler.UploadRouteRegistrar()`），避免被 `protected_routes` 的 1 MiB `MaxBodyLimit` 拦截。
  - 元数据查询、点位标定（`PUT /spatial/maps/:id/positions`）、底图读取与轨迹查询均挂入 `protected_routes`。

---

## 2. 底图格式与安全响应头

1. **格式白名单**：仅接受静态位图与矢量底图扩展名 `.png`、`.jpg`、`.jpeg`、`.webp`、`.svg`，**明确排除动图**（每帧尺寸不固定，不适合作为标定画布）。底图名称上限与 HTTP binding 一致（64 runes）。
2. **SVG 沙盒响应头（防存储型 XSS）**：
   - `GET/HEAD /api/v1/spatial/maps/:id/image` 返回原始字节流时必须附带：
     - `X-Content-Type-Options: nosniff`
     - `Cache-Control: private, max-age=86400`（隶属认证会话，禁止共享代理缓存）
     - `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox`（防止用户直接打开 SVG URL 时在宿主同源下执行内嵌脚本）。

---

## 3. 空间坐标与轨迹聚合不变量

- **归一化坐标**：`camera_canvas_positions` 使用归一化二维坐标 `(x, y)` 与朝向角 `angle`，与底图渲染分辨率解耦。
- **轨迹驻留合并窗口 (`defaultDwellWindow = 30s`)**：同一目标在同一相机上的连续识别命中，当时间间隔**严格小于 30 秒**时合并为同一轨迹节点，避免短时重复帧把轨迹折线刷碎。
- **错误码命名隔离**：遵循 [error-handling.md](./error-handling.md) 模块化 i18n 规则，业务语义冲突使用独立前缀（如 `SPATIAL_PERSON_ID_REQUIRED`），不与其他领域共用模糊 code。
