# 实施计划与步骤 (Implementation Plan)

## 阶段一：后端数据库与空间领域服务 (Backend Spatial Domain)

- [x] **1.1 数据库迁移脚本**（`000022`；`000021` 已被感知记录时序索引占用）：
  - 新建 `internal/database/migrations/000022_create_spatial_canvas_tables.up.sql`（含 `CHECK` 约束与
    级联删除）；
  - 新建 `000022_create_spatial_canvas_tables.down.sql`；
  - 编写并跑通迁移与回滚对称性测试 `TestSpatialCanvasTablesMigrationUpAndDownSymmetry`。
- [x] **1.2 空间领域模型与 Store**：
  - 创建 `internal/spatial/model.go`、`requests.go`、`responses.go`；
  - 实现 `internal/spatial/store.go`（基于 `database.DBProvider`）。
- [x] **1.3 空间领域业务服务 (SpatialService)**：
  - 底图上传、列表、删除；
  - 摄像头点位获取与批量保存更新；
  - 编写 Store 与 Service 的单元测试 `store_test.go`、`service_test.go`。
- [x] **1.4 HTTP 控制器与 Fx 依赖注入**：
  - 实现 `internal/spatial/handler.go`，使用 `internal/httputil` 信封规范；
  - 在 `internal/app` 中注册 `spatialModule`，将路由挂载至 `protected_routes` 与 `upload_routes`。

---

## 阶段二：时空轨迹引擎与聚合算法 (Trajectory Engine)

- [x] **2.1 轨迹数据查询与提取**：
  - 车辆检索：按车牌号匹配 `captures.attributes_json` 中的 `plate_number`；
  - 人员检索：按 `person_id` / `person_external_id` 匹配 `recognitions` 表；
  - 时间区间与当前地图关联摄像头过滤。
- [x] **2.2 时空窗口驻留聚合算法**：
  - 实现同摄像头连续抓拍聚合（停留时间统计、最佳快照选取）；
  - 输出有序的 `TrajectoryResponse`；
  - 编写单元测试覆盖边界条件（跨摄像头切换、单镜头长时间停留、乱序时序处理）。

---

## 阶段三：前端架构基础与电子地图页面 (Frontend E-Map Foundation)

- [x] **3.1 API 契约与客户端**：
  - 在 `web/src/features/spatial/api/spatial-api.ts` 实现 REST 请求方法；
  - 补充 Zod 模式校验与单元测试。
- [x] **3.2 路由与导航**：
  - 新增路由 `web/src/routes/_authenticated/spatial/index.tsx`；
  - 在 `web/src/components/layout/data/sidebar-data.ts` 注册「电子地图 (E-Map)」侧边栏入口；
  - 补充中英文国际化文本（`locales/zh-Hans.json`, `locales/zh-Hant.json`, `locales/en.json`）。
- [x] **3.3 画布视口核心容器 (Pan-Zoom Viewport)**：
  - 实现可平移漫游、鼠标滚轮等比缩放的画布容器；
  - 实现多底图切换与底图上传对话框。

---

## 阶段四：摄像头标定交互（拖拽、微调与验真）

- [x] **4.1 待分配摄像头资源池**：
  - 展示未放置在当前底图上的摄像头列表。
- [x] **4.2 拖拽落点与微调**：
  - 支持将摄像头从左侧列表拖拽到底图，转换为归一化相对坐标 $(u, v)$；
  - 支持画布上图标拖动微调与朝向旋转控制。
- [x] **4.3 验真与保存**：
  - 单击标定点位弹出浮层，展示实时视频流/快照核验点位；
  - 批量保存至后端。

---

## 阶段五：时空轨迹检索与动态连线回放

- [x] **5.1 轨迹检索工具栏**：
  - 支持切换【车辆轨迹 (车牌输入)】与【人员轨迹 (人员选择器)】；
  - 时间范围选择器与查询触发。
- [x] **5.2 SVG 轨迹连线渲染**：
  - 依据返回的时空节点序列，在底图上动态绘制带流动效果与箭头的 SVG 连线；
  - 标注途经序号（①, ②, ③）与抓拍悬浮卡片。
- [x] **5.3 底部时间轴播放器**：
  - 播放、暂停、倍速、时间滑块联动光标沿轨迹移动。

---

## 阶段六：质量验证与回归 (Quality Verification)

- [x] 运行 `go test -race ./internal/spatial/...` 验证后端无并发竞争（通过）；
- [x] 运行 `make go-check`（gofmt + vet + race + native 桥接测试）与 `python3 native/scripts/build.py go test -race ./internal/app`（通过）；
- [x] 运行 `pnpm exec vitest run src/features/spatial/` 验证前端单元测试全绿（通过），
      全量 `pnpm test`（725 例）+ `pnpm build` + `prettier --check` + `eslint src` 均通过；
- [x] 运行 `make api-docs` 自动生成 Swagger 文档（通过）；
- [x] 运行 `make go-check`（通过）。

---

## 阶段七：代码评审修复 (Review Fixes)

评审按 Standards / Spec 两轴逐条列出问题后的落地修复：

### 后端

- **主键改 ULID**：`generateID`（`crypto/rand` 十六进制 + `map_` 前缀）删除，改 `idutil.New()`，
  满足 `database-guidelines.md` §4.5「实体代理主键统一 ULID」与「严禁伪语义拼接复合主键」。
- **构造函数恢复纯粹**：`os.MkdirAll` 从 `NewService` 移到 `spatialLifecycleImpl.Start`，并把
  `spatial` 接入 `lifecycleRuntime`（Start 后于 Person、Stop 先于 Record），启动失败走 `degradedStart`；
  `dependency-injection.md` §2 的顺序表同步更新，`TestStartupRollbackSharesShutdownOrder` 纳入 spatial 桩。
- **不再吞错**：`rand.Read` / `MkdirAll` / 文件删除 / 文件关闭的错误要么返回、要么 `logger.Warn`，
  不再出现 `_ = fn()`（`readImageSize` 的关闭失败按读取失败处理）。
- **点位校验真正生效**：`CameraPositionItem` 加 `dive`（没有它 validator 不下钻切片元素），
  `angle` 改 `lt=360`；service 删除静默钳位，改由 422 拒绝。
- **空语义二分**：缺失 `positions` → 422 `POSITIONS_REQUIRED`；显式空数组 → 解绑全部点位。
  另新增重复摄像头（`DUPLICATE_CAMERA_POSITION`）与已删除摄像头（`CAMERA_NOT_FOUND`）的显式拒绝，
  避免外键失败被报成 500。
- **错误分类**：`Store.GetMap` 返回 `ErrMapNotFound` 域错误，service 区分 404 与 `DATABASE_ERROR`。
- **车牌检索转义** `%` / `_` / `\` 并加 `ESCAPE '\'`，同时删掉整串 `attributes_json LIKE` 兜底。
- **人员轨迹加 `matched = 1`**，未命中的识别行不再进入轨迹。
- **时间契约**：响应时间统一 `.UTC()`（零值保持零值）；`endTime > startTime` 校验；
  查询区间端点先转 UTC。
- **命名与排序**：`SnapshotURL` → `SnapshotPath`（它装的是媒体相对路径）；
  `GetPositionsByMap` 改按摄像机名排序（批量保存会重写 `created_at`，它无法表达稳定顺序）。
- **响应类型具名化**：`DeleteCanvasMapResponse` / `SaveCameraPositionsResponse` 取代 `gin.H`。
- **Swagger 注解补齐**（含 multipart 上传与图片字节流），`make api-docs` 已重新生成。
- **业务错误字典** `internal/spatial/locale.go`，13 个错误码三语注册，不再回落成「请求失败」。
- **SVG 安全与尺寸**：上传的 SVG 从 `width/height` 或 `viewBox` 读固有尺寸（读不出则 422 拒绝并清理文件）；
  读图端点加 `X-Content-Type-Options: nosniff`、`CSP ... sandbox`、`Cache-Control: private`。

### 前端

- **跨特性导入走门面**：新增 `features/live/contract.ts`、`features/persons/contract.ts`；
  `features/cameras/contract.ts` 的 `useAllCameras` 取代硬编码 `pageSize: 100`（>100 台不再静默截断）；
  `getMediaUrl` / `getGalleryImageUrl` 上移到 `@/lib/media-url`（记录域原路径改为再导出）。
- **表单纳入 react-hook-form + zod**：轨迹检索、底图上传、底图重命名三个表单，
  schema 工厂在 `data/schema.ts`，校验文案经 `t` 解析。
- **100% 文案国际化**：所有裸写中英文（含按钮 title、aria-label、空态、提示条）改为 `t(...)`，
  三语 locale 同步到 49+ 键；底图重命名入口（此前只有后端）补齐。
- **颜色走语义 token**：SVG 连线/marker 用 `stroke-primary` / `currentColor`，
  途经点高亮改用 `primary`，不再占用 amber/teal 状态色相与硬编码 `#3b82f6`。
- **准星落点**：面板点选摄像机 → 画布 `cursor-crosshair` + 顶部提示条 → 单击落点（PRD §2.2 Click & Stamp），
  保留拖拽放置。
- **缩放拖拽修正**：`camera-marker` 去掉多余的 `/zoomScale`（`getBoundingClientRect()` 已含祖先 transform 缩放），
  缩放后点位与鼠标一致。
- **平滑光标**：轨迹线段改用底图像素坐标系（`viewBox`），播放时由 `<animateMotion>` 驱动光点，
  不再逐帧 `setState`。
- **节点验真**：点位浮窗在轨迹模式下同时展示抓拍原图与到达/离开时间（PRD §2.4）。
- **三态完备**：底图列表 loading（骨架）/ error（重试）/ empty（引导），点位加载与失败各有指示与重试；
  底图删除补齐 try/catch 与 toast。
