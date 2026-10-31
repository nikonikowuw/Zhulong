# Implementation Plan: 算法包静态图片试跑与快速验证

## Ordered Implementation Checklist

### 阶段 1: 算法 SDK 报告协议扩展 (`native/sdk`)
- [x] 1.1 修改 `native/sdk/src/algo_main.cpp`：
  - 在 `run_verify` 中测量 `api.process` 的推理耗时 `cost_ms`；
  - 构造 `raw_result`（调用已有的 `serialize_inference_result`）；
  - 在 stdout 最后一行的 JSON 报告中追加 `"cost_ms": cost_ms, "result": <raw_result>`；
- [x] 1.2 验证 native SDK 构建与验包：
  - `make sdk-install` 编译 SDK；
  - 运行 `plate_recognition` 和 `face_recognition` 验包，断言最后一行输出包含 `cost_ms` 与 `result`；
  - 运行 `python3 native/sdk/tools/algo_probe.py --entry ... --verify` 确保原 probe 工具完全兼容。

### 阶段 2: Go 后端业务与 HTTP API (`internal/algorithm`)
- [x] 2.1 扩展 `internal/algorithm/verifier.go` 中的 `VerificationReport` 结构体：
  - 增加 `CostMs int64` 与 `Result *aitypes.RawInferencePayload` 字段；
- [x] 2.2 扩展 `internal/algorithm/sample_frame.go`：
  - 增加从 `io.Reader` 或字节切片直接解码图片（JPEG/PNG）、尺寸检查（≤8192px）并转写为瞬态 PPM 的工具函数；
- [x] 2.3 在 `internal/algorithm/service.go` 中实现 `TrialInference`：
  - 检查算法是否存在及其状态（非 `DISABLED` 拒绝并返回状态错误）；
  - 定位算法可执行文件 `algorunner.ResolveEntry(...)`；
  - 执行 `<entry> --verify --sample <ppm>`，限时 15 秒；
  - 解析输出并返回响应结构 `AlgorithmTrialResponse`；
- [x] 2.4 在 `internal/algorithm/handler.go` 中注册 `POST /algorithms/:id/trial`：
  - 挂载至 `UploadRoutes`，限制 10MB 请求；
  - 编写审计标签与统一信封响应；
  - 在 `locale.go` / `httputil` 补齐错误码与中英文多语言消息；
- [x] 2.5 编写单元与集成测试：
  - `service_trial_test.go`：覆盖正常检测、ACTIVE 状态拦截、损坏图片拒绝、超时终止及 PPM 文件清理；
  - `handler_trial_test.go`：覆盖 HTTP 400 / 404 / 409 / 413 / 200 信封。

### 阶段 3: 前端交互与 Canvas 画框渲染 (`web/src/features/algorithms`)
- [x] 3.1 扩展 API 与 Schema：
  - `web/src/features/algorithms/data/schema.ts` 补充 `algorithmTrialResponseSchema`；
  - `web/src/features/algorithms/api/algorithms-api.ts` 增加 `trialAlgorithm(id, file)`；
  - 多语言文件（`zh-Hans.json`, `en.json`, `zh-Hant.json`）补充试跑相关文案；
- [x] 3.2 实现 `AlgorithmTrialDialog`：
  - 图片上传/拖拽区域与原图预览；
  - 基于 Canvas 的高精度等比画框层；
  - 目标类别高亮、置信度条与统计卡片；
  - 「下载带框图片」按钮；
- [x] 3.3 在表格操作列接入：
  - `web/src/features/algorithms/components/algorithms-data-table-row-actions.tsx` 添加「效果试跑」操作选项；
  - `web/src/features/algorithms/components/algorithms-dialogs.tsx` 装配弹窗；
- [x] 3.4 编写前端组件自动化测试：
  - `algorithm-trial-dialog.test.tsx` 验证对话框渲染、文件选择、API 触发与 Canvas 渲染。

### 阶段 4: 契约文档与全仓回归
- [x] 4.1 重新生成 Swagger API 文档：`make api-docs`；
- [x] 4.2 运行全量门禁：`make check`。

---

## Validation Commands

```bash
# 1. SDK 校验
make sdk-install
./packages/plate_recognition/onnx/build/plate_recognition --verify --sample packages/plate_recognition/onnx/sample.jpg

# 2. Go 算法模块测试
python3 native/scripts/build.py go test -race -v ./internal/algorithm -run TestTrial

# 3. 前端测试
cd web && pnpm test -- src/features/algorithms/components/algorithm-trial-dialog.test.tsx

# 4. 全量验证
make check
```

---

## Risky Files & Rollback Points

| 文件 | 风险点 | 防控措施 |
| :--- | :--- | :--- |
| `native/sdk/src/algo_main.cpp` | `--verify` 是安装验包的核心链路，改错会影响所有算法包准入 | 只追加 JSON 键，不破坏已有字段；`algo_probe.py` 回归验证 |
| `internal/algorithm/handler.go` | 挂载在 `UploadRoutes`，需防范大文件与畸形文件 | 严格限定 10MB，必须在内存解码前校验 header 与尺寸 |
| 临时 PPM 写入路径 | 磁盘泄漏风险 | 采用 `defer os.Remove(...)`，无论退出分支如何确保销毁 |
| 前端 Canvas | Retina 屏幕坐标错位或模糊 | 使用 `devicePixelRatio` 统一缩放，统一利用归一化坐标计算 |
