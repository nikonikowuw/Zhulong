# 人员人脸工业级压缩包批量导入 实施计划 (Implementation Plan)

## 1. 数据库与领域模型 (Database & Domain Models)

- [x] 在 `internal/database/migrations/` 新增 `000024_create_person_face_import_jobs.up.sql` 与 `down.sql`（按顺序递增编号）。
- [x] 在 `internal/person` 定义 Job 模型 `PersonFaceImportJob` 与状态枚举（`Pending`, `Extracting`, `Processing`, `Completed`, `Failed`, `Cancelled`）。
- [x] 扩展 `PersonStore` 接口及 GORM 实现：支持 Job 创建、状态/进度原子更新、查询及失败明细写入。

## 2. 安全解压与 AI 质检门禁 (Zip Safety & AI Gate)

- [x] 实现 ZIP 安全解压器（Zip Slip 校验、Zip Bomb 动态字节限制、目录层级防爆、垃圾隐藏文件忽略）。
- [x] 实现 Manifest 解析器与自适应文件名正则扫描器（支持 `manifest.csv` 与 `工号_姓名.jpg` 两种模式）。
- [x] 实现图像质检门禁流水线：
  - [x] 像素防爆（`image.DecodeConfig` 校验宽高 $\le 4096$）。
  - [x] SCRFD 人脸检出计数校验（0 检出 $\to$ `NO_FACE_DETECTED`，$\ge 2$ 检出 $\to$ `MULTIPLE_FACES_DETECTED`）。
  - [x] 姿态角度与外接框尺寸校验（大偏角 $\to$ `FACE_POSE_EXCEED`，短边 $< 80\text{px}$ $\to$ `FACE_TOO_SMALL`）。
  - [x] ArcFace 特征建模（提取 512 维特征向量）。

## 3. 异步任务调度与底库批量同步 (Async Worker & QoS)

- [x] 实现 `FaceImportWorker` 后台协程队列：
  - [x] 单 Worker 串行/受控调度，推理间隔微秒级让渡（QoS 算力防饿死）。
  - [x] 事务性插入人员与一人多照绑定（支持 `APPEND` 追加多照）。
  - [x] 任务结束聚合增量底库，触发单次 IPC 热更新广播。
  - [x] 临时解压目录 RAII 自动清除与启动孤儿目录清理扫描器。

## 4. HTTP API 路由与契约

- [x] 实现 `POST /api/v1/persons/import-face-jobs`（上传 ZIP，立即响应 202 Accepted）。
- [x] 实现 `GET /api/v1/persons/import-face-jobs/:jobId`（查询任务进度）。
- [x] 实现 `GET /api/v1/persons/import-face-jobs/:jobId/failed-report`（下载失败诊断 CSV）。
- [x] 注册路由到 `upload_routes` 并更新 Swagger 文档（`make api-docs`）。

## 5. 前端交互与组件实现 (React SPA)

- [x] 在 `web/src/features/persons/api/persons-api.ts` 新增异步任务 API 客户端函数。
- [x] 在 `web/src/features/persons/components/` 实现 `persons-face-import-dialog.tsx`：
  - [x] ZIP 拖拽上传与标准模板说明。
  - [x] 异步进度条展示（轮询刷新状态与进度比例）。
  - [x] 完成卡片与失败报告 CSV 下载。
- [x] 完善简中、繁中、英文 i18n 本地化文案。
- [x] 编写前端组件自动化测试 `persons-face-import-dialog.test.tsx`。

## 6. 验证与门禁测试 (Validation & Checks)

- [x] 编写 Go 单元测试与集测试：覆盖安全解压（Zip Slip / Bomb 防御）、AI 质检门禁、QoS Worker 及 API Handler。
- [x] 运行 `make go-check`（编译、vet、集成测试、-race 检查）。
- [x] 运行 `make frontend-check`（类型检查、lint、格式检查、真实无头浏览器测试）。
- [x] 运行 `make api-docs`，核对接口文档契约。

## Validation Commands

```bash
# 1. 运行人脸批量导入单元测试
go test -race -v ./internal/person/...

# 2. 运行前端组件测试
pnpm --dir web vitest run src/features/persons/components/persons-face-import-dialog.test.tsx

# 3. 运行全套门禁
make go-check
make frontend-build
```
