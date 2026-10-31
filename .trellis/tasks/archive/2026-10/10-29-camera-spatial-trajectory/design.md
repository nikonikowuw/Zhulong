# 电子地图与时空轨迹技术设计 (Design Document)

## 1. 总体架构与数据流 (Architecture & Data Flow)

```text
               +----------------------------------------------------+
               |                Web 前端 (React + SPA)              |
               |   [侧边栏: E-Map] -> /_authenticated/spatial       |
               +-------------------------+--------------------------+
                                         | REST API (httputil 信封)
                                         v
               +----------------------------------------------------+
               |             Go 边缘宿主 (internal/spatial)          |
               |                                                    |
               |   +-------------------+    +-------------------+   |
               |   |   SpatialService  |    | TrajectoryEngine  |   |
               |   |  - 底图与点位管理  |    |  - 时空去重与聚合  |   |
               |   +---------+---------+    +---------+---------+   |
               +-------------|------------------------|-------------+
                             |                        |
                   GORM / SQLite Provider             |
                             v                        v
            +----------------------------------+--------------------+
            | canvas_maps / camera_positions   | captures / records |
            +----------------------------------+--------------------+
```

---

## 2. 数据库设计 (Database Schema & Migration)

新建迁移 `internal/database/migrations/000022_create_spatial_canvas_tables.up.sql`（`000021` 已被感知记录时序索引占用）：

```sql
-- 1. 电子地图底图表
CREATE TABLE IF NOT EXISTS canvas_maps (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    image_path TEXT NOT NULL,
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 2. 摄像头在底图上的标定位置表
CREATE TABLE IF NOT EXISTS camera_canvas_positions (
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    map_id TEXT NOT NULL REFERENCES canvas_maps(id) ON DELETE CASCADE,
    x REAL NOT NULL CHECK (x >= 0.0 AND x <= 1.0),        -- 归一化 X [0.0, 1.0]
    y REAL NOT NULL CHECK (y >= 0.0 AND y <= 1.0),        -- 归一化 Y [0.0, 1.0]
    angle REAL NOT NULL DEFAULT 0.0
        CHECK (angle >= 0.0 AND angle <= 360.0),          -- 朝向角 [0, 360)，API 层拒绝 360
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (camera_id, map_id)
);

CREATE INDEX IF NOT EXISTS idx_camera_positions_map ON camera_canvas_positions(map_id);
```

回滚脚本 `000022_create_spatial_canvas_tables.down.sql`：
```sql
DROP INDEX IF EXISTS idx_camera_positions_map;
DROP TABLE IF EXISTS camera_canvas_positions;
DROP TABLE IF EXISTS canvas_maps;
```

主键 `canvas_maps.id` 由 `internal/idutil.New()` 生成（纯 ULID）：它是 SQLite B-Tree 的排序键，
随机十六进制串会导致高频页分裂（`database-guidelines.md` §4.5）。

---

## 3. 后端领域与 API 契约 (Backend API & Contracts)

### 3.1 领域模型 (Go Structs in `internal/spatial`)
遵循项目规范：禁止直接收发 GORM 模型，命名使用 `*Request` / `*Response` / `*Info`。

```go
type CanvasMapResponse struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    ImageURL  string    `json:"imageUrl"`
    Width     int       `json:"width"`
    Height    int       `json:"height"`
    CreatedAt time.Time `json:"createdAt"`
    UpdatedAt time.Time `json:"updatedAt"`
}

type CameraPositionInfo struct {
    CameraID   string  `json:"cameraId"`
    CameraName string  `json:"cameraName"`
    X          float64 `json:"x"`
    Y          float64 `json:"y"`
    Angle      float64 `json:"angle"`
}

// dive 不可省：validator 不会下钻 []struct 的元素规则，没有它 gte/lte 形同虚设。
type CameraPositionItem struct {
    CameraID string  `json:"cameraId" binding:"required"`
    X        float64 `json:"x" binding:"gte=0,lte=1"`
    Y        float64 `json:"y" binding:"gte=0,lte=1"`
    Angle    float64 `json:"angle" binding:"gte=0,lt=360"`
}

// 缺失 positions（nil）→ 422 POSITIONS_REQUIRED；显式空数组 → 解绑全部点位。
type BatchSavePositionsRequest struct {
    Positions []CameraPositionItem `json:"positions" binding:"dive"`
}

// 轨迹查询请求
type TrajectoryQueryRequest struct {
    MapID      string     `form:"mapId" binding:"required"`
    TargetType string     `form:"targetType" binding:"required,oneof=vehicle person"`
    Plate      string     `form:"plate"`     // 车辆时生效
    PersonID   string     `form:"personId"`  // 人员时生效
    StartTime  time.Time  `form:"startTime" binding:"required" time_format:"2006-01-02T15:04:05Z07:00"`
    EndTime    time.Time  `form:"endTime" binding:"required" time_format:"2006-01-02T15:04:05Z07:00"`
}

// 轨迹时空节点
type TrajectoryNodeInfo struct {
    Seq         int       `json:"seq"`
    CameraID    string    `json:"cameraId"`
    CameraName  string    `json:"cameraName"`
    X           float64   `json:"x"`
    Y           float64   `json:"y"`
    FirstSeenAt time.Time `json:"firstSeenAt"`
    LastSeenAt  time.Time `json:"lastSeenAt"`
    DwellSec    int       `json:"dwellSec"`
    SnapshotPath string   `json:"snapshotPath"`  // 媒体相对路径，前端经 /api/v1/media/content/* 访问
    Score       float64   `json:"score"`
    Details     string    `json:"details,omitempty"`
}

type TrajectoryResponse struct {
    MapID      string               `json:"mapId"`
    TargetType string               `json:"targetType"`
    TargetKey  string               `json:"targetKey"`
    Nodes      []TrajectoryNodeInfo `json:"nodes"`
}
```

### 3.2 HTTP 端点路由 (挂载于 `protected_routes`)
- `GET /api/v1/spatial/maps`：获取所有底图列表
- `POST /api/v1/spatial/maps`：创建底图（`upload_routes`，multipart 上传并解析固有尺寸）
- `GET /api/v1/spatial/maps/:id`：单张底图元数据
- `PATCH /api/v1/spatial/maps/:id`：重命名底图（返回更新后的完整记录）
- `DELETE /api/v1/spatial/maps/:id`：删除底图（级联解绑该底图点位）
- `GET|HEAD /api/v1/spatial/maps/:id/image`：底图字节流（携带 `X-Content-Type-Options: nosniff`
  与 `Content-Security-Policy: ... sandbox`，防止用户上传的 SVG 在同源下执行脚本）
- `GET /api/v1/spatial/maps/:id/positions`：获取该底图所有已标定摄像头点位
- `PUT /api/v1/spatial/maps/:id/positions`：批量替换该底图上的摄像头点位
- `GET /api/v1/spatial/trajectory`：时空轨迹查询接口（车牌/人员 + 时间段 + mapId）

### 3.3 时空清洗与聚合算法 (Trajectory Aggregator)
1. **数据源拉取**：
   - 若 `TargetType == "vehicle"`：查 `captures` 表，过滤 `target_type = 'vehicle'` 且 `attributes_json` 包含该车牌号；
   - 若 `TargetType == "person"`：查 `recognitions` 表，过滤 `person_id = :personId` 且 `matched = 1`；
   - 过滤条件附加时间范围 `captured_at BETWEEN startTime AND endTime`；
   - 结果按 `captured_at ASC` 排序。
2. **空间过滤**：
   - 仅保留当前 `map_id` 下已标定坐标的摄像头事件，忽略未在该地图上的摄像头。
3. **时空窗口聚合（Dwell Aggregation）**：
   - 遍历抓拍列表，维护当前活跃节点：
     - 若当前抓拍的 `camera_id` 与上一条相同，且 `captured_at - prev.captured_at < 30s`：
       - 更新 `last_seen = captured_at`；
       - 若当前抓拍置信度更高，则替换候选 `snapshot_url`；
     - 若 `camera_id` 发生变更或时间跨度超出了阈值：
       - 将上一节点封口追加至 `nodes`，并开启新节点；
   - 输出有序数组 `nodes`，赋予自增序号 `seq = 1, 2, ...`。
4. **请求侧前置校验**：`endTime > startTime`（否则 422 `INVALID_TIME_RANGE`）；区间端点统一转 UTC
   后再与 `captured_at` 比较；人员检索额外要求 `matched = 1`，未命中的识别行不参与轨迹。

---

## 4. 前端架构与交互设计 (Web Frontend)

### 4.1 目录切片设计 (`web/src/features/spatial/`)
```text
web/src/features/spatial/
├── api/
│   ├── spatial-api.ts          # API 客户端请求
│   └── spatial-api.test.ts
├── components/
│   ├── canvas/
│   │   ├── spatial-canvas.tsx      # 画布核心容器 (缩放、平移手势、准星落点)
│   │   ├── camera-marker.tsx       # 摄像头点位图标 (拖拽/朝向扇形)
│   │   └── trajectory-layer.tsx    # SVG 轨迹线、流动动画与序号标注
│   ├── toolbar/
│   │   ├── map-switcher.tsx        # 底图下拉切换 & 上传管理
│   │   ├── trajectory-search-form.tsx # 车辆(车牌)/人员(底库)轨迹检索（react-hook-form + zod）
│   │   ├── map-upload-dialog.tsx   # 底图上传（react-hook-form + zod）
│   │   ├── map-rename-dialog.tsx   # 底图重命名
│   │   └── calibration-toolbar.tsx # 点位标定工具条 (保存/重置)
│   ├── sidebar/
│   │   └── unassigned-cameras-panel.tsx # 待分配摄像头池 (拖出放置 + 点选准星)
│   ├── playback/
│   │   └── trajectory-timeline-player.tsx # 底部浮动轨迹时间轴播放器
│   └── preview/
│       └── camera-live-popover.tsx # 单击点位弹出实时预览 + 轨迹抓拍/时间戳
├── hooks/
│   ├── use-spatial-maps.ts
│   ├── use-camera-positions.ts
│   └── use-trajectory.ts
├── data/
│   └── schema.ts
├── locales/
│   ├── zh-Hans.json
│   ├── zh-Hant.json
│   └── en.json
└── index.tsx                       # 页面顶层容器
```

### 4.2 画布交互技术选型与实现
- **视口交互（Pan & Zoom）**：采用轻量手势驱动平移和滚轮缩放，保持底图居中自适应。
- **坐标变换计算**：
  $$\text{相对坐标 } (u, v) \iff \text{屏幕点击 } (X, Y)$$
  通过 `getBoundingClientRect()` 与当前的 `transform (scale, translate)` 进行矩阵逆变换。
- **SVG 轨迹流动动画**：
  - 相邻两点 $(x_1, y_1) \to (x_2, y_2)$ 以**底图固有像素**为单位绘制（`viewBox="0 0 W H"`），
    不用百分比：`<animateMotion path>` 与箭头 marker 都需要用户单位；
  - 样式使用 `stroke-dasharray: 8 4` 与 CSS 动画 `stroke-dashoffset` 呈现光点流动行进效果；
  - 终点绘制有向箭头 Marker；
- **播放器联动**：
  - 时间滑块拖动或自动播放时高亮当前节点与所在线段；沿线段平滑移动的光标由 SVG `animateMotion` 驱动，
    **不经过 React 状态**——逐帧 `setState` 会让整张画布（底图 + 全部点位）跟着重渲染。
