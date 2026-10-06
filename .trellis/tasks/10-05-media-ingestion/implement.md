# 实施计划：媒体接入与流转管道

> 对应 PRD: `.trellis/tasks/10-05-media-ingestion/prd.md`
> 对应 Design: `.trellis/tasks/10-05-media-ingestion/design.md`

---

## 阶段一：Native C++ 原生底座与 C ABI 门面 (Phase 1: Native & ABI)

- [ ] **Step 1.1: CMake 依赖配置与裁剪版 FFmpeg 静态集成**
  - 在 `native/CMakeLists.txt` 中配置静态链接 `libavformat.a`, `libavcodec.a`, `libavutil.a` (或通过 `find_package(PkgConfig)` / `pkg_check_modules` 优先使用静态归档)
  - 编写最小 demo 测试链接与符号可见性
  - *验证命令*：`make native-build && make native-test`
- [ ] **Step 1.2: C ABI 扩展与头文件声明**
  - 在 `native/include/Zhulong/engine.h` 中扩展探测、流会话管理、订阅回调及元数据结构体
  - 编写 `native/tests/engine_abi_test.c` 验证纯 C 编译器兼容性
  - *验证命令*：`ctest --test-dir build/native -R ZhulongCAbiTests`
- [ ] **Step 1.3: `StreamSourcePool` 物理流连接池与引用计数实现**
  - 实现 URL 标准化工具函数 `NormalizeRtspUrl`
  - 实现 `PhysicalStreamSession` 与 `StreamSourcePool`，封装 `AVIOInterruptCB` 超时中断机制
  - 实现 8 秒 Grace Period 延时休眠定时器
  - 编写 C++ 单元测试 `tests/stream_pool_test.cpp` 覆盖多订阅者接入、计数递减与倒计时取消/触发
  - *验证命令*：`ctest --test-dir build/native -R StreamPool`
- [ ] **Step 1.4: 独立探测器 `Zhulong_stream_probe` 实现**
  - 实现带独立超时时间窗的单次拉流探测器，解析宽、高、FPS 与 H.264/H.265 Codec
  - 编写异常流（错误端口、不可达 IP）边界测试
  - *验证命令*：`make native-test`

---

## 阶段二：Go 业务模块与 CGO 跨层桥接 (Phase 2: Backend & CGO)

> 2026-10-05：用户选择 A，保留 Native 交叉/板端待验收项，先在宿主推进本阶段。
> 实施顺序：**桥接 → 摄像机业务 → WebSocket 分发**，每项独立规划、实现与检查。
> Step 2.1 由 `.trellis/tasks/10-05-go-cgo-media-bridge/` 承接；已完成并归档，实现提交 `8a6a887`，宿主验收见开发日志。
> Step 2.2/2.3 与业务状态机/退避/SSE 由 `10-05-camera-lifecycle` 承接，用户已批准创建并进入规划，尚未批准实现；Step 2.4 仍留给后续独立子任务。
> 下方旧示例命令/签名由各子任务基于实际代码收敛，Go 验证须使用 `native/scripts/build.py go ...` 或 Make 注入静态链接。认证使用现有 Cookie Session，不引入草案 JWT。

- [x] **Step 2.1: CGO 门面与按需订阅借用实现**
  - 在 `internal/engine/engine.go` 封装新增的 C ABI
  - 实现 `cgo.Handle` 安全注册与注销机制，使用 `unsafe.Slice` 借用数据切片
  - 编写 Go-CGO 单元测试，确保无内存逃逸与指针越权
  - *验证命令*：`go test -race -v ./internal/engine/...`
- [x] **Step 2.2: 摄像机数据模型与 SQLite 迁移**
  - 创建 `internal/database/migrations/000003_create_cameras_table.up.sql`
  - 定义 `Camera` GORM 实体与凭据安全存储（AES-GCM 加密），DTO 支持为已认证管理员返回可读凭据，日志输出强制脱敏
  - 注册数据库迁移并验证向上/向下兼容
  - *验证命令*：`go test -v ./internal/database/...`
- [x] **Step 2.3: `internal/camera` 业务服务与探测门禁**
  - 实现 `ParseAndSanitizeRTSP` 逆向消歧与 RFC 3986 百分号转义算法（输出规范 URL、脱敏 URL、用户名与密码），编写特殊字符测试用例
  - 实现 `CameraStore` 与 `CameraService`（详情接口向已登录管理员解密返回完整可用 RTSP URL 与密码）
  - 实现 `CreateCamera` 流程中的 3~5s 异步 Probe 探测门禁，错误配置拦截返回对应语义错误
  - 注册 `locale.go` 错误码三语翻译映射
  - *验证命令*：`go test -race -v ./internal/camera/...`
- [x] **Step 2.4: WebSocket 码流广播中心与 HTTP 端点**
  - 实现 `StreamHub` 广播器，维护当前通道的 Web 订阅连接列表
  - 实现 `/api/v1/cameras/:id/stream/ws` 端点（集成 JWT 鉴权与心跳保活）
  - 生成并更新 Swagger 2.0 文档
  - *验证命令*：`make swagger && make check`

---

## 阶段三：前端功能解耦与全场景播放器 (Phase 3: Frontend Web)

- [ ] **Step 3.1: 独立播放器组件 `LivePlayer.tsx` 与前端流池实现 (`web/src/features/live/`)**
  - 实现前端单例流连接池 `FrontendStreamPool` 与 `useCameraStream` Hook（维护纯客户端 `refCount`，实现分屏视口复用）
  - 集成支持 H.264/H.265 的三级自适应播放方案（WebCodecs ➔ MSE ➔ WASM）
  - 实现断线重连动画、缓冲加载与延迟状态指示
  - 编写 `RoiOverlayCanvas.tsx` 支撑后续 AI 检测框绘制
  - 编写前端流池引用计数单元测试（验证同一相机多视口挂载仅开单条 WS，视口归零销毁 WS）
  - *验证命令*：`cd web && npm run type-check && npm run lint && npm test`
- [x] **Step 3.2: 摄像机管理独立菜单与视图 (`web/src/features/camera/` ➔ `/cameras`)**
  - 实现摄像机列表表格、增删改查弹窗（支持结构化与单行 URL 两种输入）
  - 实现密码明文显隐（小眼睛 👁️）与一键复制完整可用 RTSP 地址功能，方便外部播放器调试
  - 实现入库前 3~5s 异步探测 Loading 与结果回显
  - 接入 `useCamerasQuery` 与 `useCreateCameraMutation`
  - *验证命令*：`cd web && npm test`
- [ ] **Step 3.3: 实时预览独立菜单与宫格看板 (`web/src/features/live/` ➔ `/live`)**
  - 实现 1/4/9 宫格自适应切换控制栏与全屏模式
  - 绑定摄像机 WebSocket 码流端点，支持多通道并行拉流与按需生命周期
  - 接入顶栏菜单导航路由拆分（`/cameras` vs `/live`），支持中/英/繁三语
  - *验证命令*：`cd web && npm run build`

---

## 阶段四：全链路联调与验收门禁 (Phase 4: Verification & Integration)

- [ ] **Step 4.1: 端到端功能与物理流复用验收**
  - 搭建本地 RTSP 模拟源（包含 H.264 与 H.265 码流）
  - 验证多媒体对象复用同一流、无用户查看时延迟休眠、断网指数退避重连
  - *验证命令*：`make run` 配合测试用例执行
- [ ] **Step 4.2: 内存泄漏与竞态全面体检**
  - 执行 `go test -race ./...` 与 ASan/Valgrind 原生内存检查
  - *验证命令*：`make check`
