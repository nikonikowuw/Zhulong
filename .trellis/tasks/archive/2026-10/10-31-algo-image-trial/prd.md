# PRD: 算法包静态图片试跑与快速验证 (Algorithm Image Trial Inference)

## Goal

在 Web 管理页支持对已安装且处于「已停用」(DISABLED) 状态的算法包上传单张静态图片执行单次试跑推理，获取结构化检测结果并在前端图片上通过 Canvas 动态叠加绘制检测框与属性，供运维与算法人员快速直观验证算法检测效果。

---

## Background & Confirmed Facts

1. **算法包交付形态已全面可执行文件化**：
   - 算法包以独立可执行文件交付（`<pkgDir>/<manifest.id>`，静态链接运行时），宿主通过命令行参数与其交互，不通过 `dlopen` 加载算法动态库（`.trellis/spec/native/algo-plugin-contract.md`）。
2. **协议与验包基础**：
   - 算法包入口支持 `--verify --sample <ppm_path>` 进行单次生命周期推理（`native/sdk/src/algo_main.cpp` 的 `run_verify`）；
   - 包入口原生只解码 netpbm（PPM P6 / PGM P5），不包含 JPEG/PNG 解码库；Go 宿主在 `internal/algorithm/sample_frame.go` 中已有通过标准库解码 JPEG/PNG 并转写为临时 PPM P6 的成熟路径；
   - 算法包内部已有成熟的单帧推理结果序列化函数 `serialize_inference_result`，支持 4 种标准载荷（`detection`、`classification`、`person_structure`、`vehicle_structure`），坐标全系统严格归一化为 `[x1, y1, x2, y2] ∈ [0.0, 1.0]`。
3. **架构与资源约束**：
   - 试跑仅限定在「已停用」(DISABLED) 状态的算法包，避免与线上实时推理进程抢占 NPU 上下文与内存。
   - 路由走 `UploadRoutes`（已有认证、审计、请求体限额防护）。
   - 试跑产生的图片与推理结果纯属内存/瞬态操作，不落库、不生成告警与抓拍记录。

---

## Requirements

### R1. 算法 SDK / 可执行文件输出结构化结果
- 在 `native/sdk/src/algo_main.cpp` 的 `run_verify` 中，测量单次前向推理耗时 `cost_ms`。
- 在成功执行推理后，除了输出现有的 `status`、`sample_decoded`、`width`、`height`、`result_type`、`count` 之外，将 `serialize_inference_result` 生成的完整结构化结果序列化至最后一行 JSON 报告的 `result` 字段，并输出 `cost_ms`。
- 保持向后兼容：输出格式为只追加字段，Go 既有验包门禁和 Python `algo_probe.py` 仍完全兼容。

### R2. 宿主 Go 后端试跑 API
- 在 `internal/algorithm/handler.go` 注册 `POST /algorithms/:id/trial` 端点（挂载至 `UploadRoutes`）。
- **状态守卫**：
  - 算法不存在返回 404 `ALGORITHM_NOT_FOUND`；
  - 算法处于 `ACTIVE` 状态返回 409 `ALGORITHM_IS_ACTIVE`（提示算法正在运行，需先停用后再进行单次试跑）；
  - 算法处于 `CRASH_LOOP` 状态返回 409 `ALGORITHM_IN_CRASH_LOOP`（提示先重置熔断）。
- **输入校验**：
  - 上传单张图片文件（`file`），支持 JPEG (`image/jpeg`) 与 PNG (`image/png`)；
  - 单图上传体积上限 10MB（超出返回 413），图像宽高上限 8192px（超出返回 400）。
- **执行推理**：
  1. 使用 Go 标准库解码图片并转写为瞬态 PPM 临时文件（复用/扩展 `sample_frame.go`）；
  2. 使用 `exec.CommandContext` 并在算法安装目录下以受控超时（15 秒）执行 `<entry> --verify --sample <temp_ppm>`；
  3. `defer` 确保无论成功失败均及时删除临时 PPM 文件；
  4. 解析最后一行 JSON 报告，提取耗时 `cost_ms`、图像宽高等以及结构化检测结果；
  5. 组装标准 `httputil.Response` 信封返回 `AlgorithmTrialResponse`。

### R3. 前端 Web 管理界面交互与 Canvas 画框
- 在算法管理页（`web/src/features/algorithms`）的行操作菜单（`AlgorithmsDataTableRowActions`）中增加「效果试跑」操作项（仅在非 ACTIVE 状态下可用）。
- 点击弹出「算法效果试跑」对话框（`AlgorithmTrialDialog`）：
  - 支持拖拽或选择本地图片（JPG/PNG）上传预览；
  - 提供辅助提示：「仅供静态推理效果验证；不包含跨帧跟踪与底库人脸比对」；
  - 点击「开始试跑」后调用 API，显示加载动画；
  - 试跑完成后：在界面中展示原始图片，并在其上方通过 HTML5 Canvas 依据归一化坐标 `[x1, y1, x2, y2]` 自适应高精度绘制目标框；
  - 针对不同任务类型适配绘制样式：
    - 通用目标检测：绘制矩形框、置信度分数与类别标签；
    - 人体/人脸结构化：区分全身框（绿色）与人脸框（橙色），标明人脸关键点或质量分；
    - 机动车/车牌结构化：区分车辆框（绿色）与车牌框（蓝色），标注识别出的车牌号与车牌颜色；
    - 分类算法：在侧边面板以 Badge 清单展示类别命中与置信度；
  - 提供「下载带框图」功能（由 Canvas 原生合成导出 JPEG 下载）。

---

## Out of Scope (非目标)

- 不支持对运行中 (`ACTIVE`) 的算法包进行热流打断式试跑。
- 不持久化试跑上传的图片，不生成正式业务抓拍、告警或通行记录。
- 不在试跑中提供 1:N 人员底库比对能力（底库比对依赖实时进程的特征索引与同步广播）。
- 不支持视频文件或动态流上传试跑（视频流验证通过常规通道任务配置）。

---

## Acceptance Criteria

- [ ] **AC1**：算法 SDK 执行 `<manifest.id> --verify --sample <image.ppm>` 时，最后一行 JSON 报告输出包含 `cost_ms` 与完整的 `result` 结构化载荷。
- [ ] **AC2**：调用 `POST /api/v1/algorithms/:id/trial`，当算法处于 `ACTIVE` 状态时被拦截并返回 409 `ALGORITHM_IS_ACTIVE`。
- [ ] **AC3**：在算法处于 `DISABLED` 时上传合法 JPG/PNG 图片，接口在超时内返回 `cost_ms`、`width`、`height` 与结构化检测目标。
- [ ] **AC4**：前端上传图片试跑后，Canvas 能自适应图片原始宽高比精确画出目标矩形框，标签与分数清晰可见，无坐标拉伸错位，并支持下载带框图片。
- [ ] **AC5**：临时生成的 PPM 文件无论推理成功或失败均被正确清理，无磁盘泄漏或孤儿进程。
- [ ] **AC6**：`make check` 全绿（前端 lint/type-check/test + native-test + api-docs + go-check）。
