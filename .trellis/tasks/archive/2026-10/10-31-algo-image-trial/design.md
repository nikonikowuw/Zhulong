# Design: 算法包静态图片试跑与快速验证 (Algorithm Image Trial Inference)

## 1. 架构定位与系统边界

本功能旨在解决**算法部署后无需绑定摄像机和配置任务通道，即可上传任意单张静态图片快速验证推理效果与检出目标**的工程诉求。

```
 [浏览器 Web 管理页]
      │
      │ 1. 上传图片 (POST /api/v1/algorithms/:id/trial)
      ▼
 [Go 宿主 (internal/algorithm)]
      │
      ├─ 状态校验 (确保算法已安装且处于 DISABLED 状态)
      ├─ 图片格式与尺寸安全校验 (JPEG/PNG, ≤10MB, ≤8192px)
      ├─ 转写为瞬态 Netpbm PPM (P6)
      │
      │ 2. 子进程调用 (<pkgDir>/<manifest.id> --verify --sample <ppm>)
      ▼
 [算法包独立进程 (<manifest.id>)]
      │
      ├─ load_sample_frame (PPM 原生解码为 RGB24 内存)
      ├─ 单次前向推理 (model_create -> worker_create -> task_open -> process)
      ├─ 序列化结构化结果 (serialize_inference_result)
      │
      │ 3. stdout 输出单行 JSON 报告 (status: passed, cost_ms: ..., result: {...})
      ▼
 [Go 宿主 (internal/algorithm)]
      │
      ├─ 清理临时 PPM 文件 (defer cleanup)
      ├─ 解析 JSON 报告并装配为 AlgorithmTrialResponse
      │
      │ 4. HTTP JSON 响应
      ▼
 [浏览器 Web 管理页]
      │
      └─ HTML5 Canvas 读取原始上传图并在其表面高精度叠加绘制归一化目标框及信息
```

---

## 2. 核心协议与契约设计

### 2.1 算法 SDK `--verify` 契约增强 (`native/sdk/src/algo_main.cpp`)

在 `algo_main.cpp` 的 `run_verify` 中：
1. 使用 `std::chrono::steady_clock` 记录前向推理（`api.process`）的耗时 `cost_ms`；
2. 保持对已有字段的兼容，最后一行 JSON 报告格式扩展为：

```json
{
  "status": "passed",
  "sample": "/tmp/zhulong_trial_xxx.ppm",
  "sample_decoded": true,
  "width": 1920,
  "height": 1080,
  "result_type": 1,
  "count": 2,
  "cost_ms": 35,
  "result": {
    "task_type": "detection",
    "detections": [
      { "box": [0.1, 0.2, 0.3, 0.4], "label": "person", "score": 0.92 }
    ],
    "classifications": [],
    "persons": [],
    "vehicles": []
  }
}
```

> **字段兼容性保证**：`result` 和 `cost_ms` 均为尾部追加字段。原有的 Go 侧验包器 `Verifier`（仅取 `status`, `sample_decoded`, `result_type`, `count`）与 Python 验包脚本 `algo_probe.py` 将继续正常工作。

### 2.2 Go HTTP API 契约

- **端点**：`POST /api/v1/algorithms/:id/trial`
- **路由组**：`UploadRoutes`（享受 500MB 全局限额、Auth 鉴权与审计中间件），业务代码层强制限制为 10MB。
- **请求内容**：`multipart/form-data`，参数名 `file`。

#### 响应载荷 (`AlgorithmTrialResponse`)

```go
type AlgorithmTrialResponse struct {
    CostMs          int64                      `json:"cost_ms"`
    Width           int                        `json:"width"`
    Height          int                        `json:"height"`
    TaskType        string                     `json:"task_type"`
    Detections      []aitypes.DetectionItem    `json:"detections"`
    Classifications []aitypes.ClassificationItem `json:"classifications"`
    Persons         []aitypes.PersonItem       `json:"persons"`
    Vehicles        []aitypes.VehicleItem      `json:"vehicles"`
}
```

#### 错误码定义与 HTTP 状态码映射

| 场景 | HTTP 状态码 | 业务错误码 | 说明 |
| :--- | :--- | :--- | :--- |
| 算法不存在 | 404 | `ALGORITHM_NOT_FOUND` | 指定 ID 的算法未安装 |
| 算法处于 ACTIVE | 409 | `ALGORITHM_IS_ACTIVE` | 算法正在运行，需停用后再试跑 |
| 算法处于 CRASH_LOOP | 409 | `ALGORITHM_IN_CRASH_LOOP` | 算法已熔断，需先重置 |
| 上传文件不是图片 | 400 | `INVALID_IMAGE_FILE` | 仅支持 JPEG 或 PNG 格式 |
| 图片体积超限 (>10MB) | 413 | `FILE_TOO_LARGE` | 文件超过 10MB |
| 图像尺寸超限 (>8192px)| 400 | `IMAGE_DIMENSIONS_TOO_LARGE` | 宽度或高度超出 8192 像素 |
| 推理执行超时 (15s) | 504 | `TRIAL_TIMEOUT` | 子进程执行超时已终止 |
| 算法内部推理失败 | 500 | `TRIAL_EXECUTION_FAILED` | 算法可执行文件退出码非 0 |

---

## 3. 前端交互与 Canvas 动态绘制设计

### 3.1 交互流程
1. 用户在算法列表的操作列下拉菜单中点击「效果试跑」（若算法为 `ACTIVE` 则置灰并提示「需先停用算法包」）；
2. 弹出 `AlgorithmTrialDialog`：
   - 包含拖拽/点击上传选区，支持展示图片预览；
   - 标注「仅供单帧检测效果验证，不包含跨帧跟踪与底库人脸比对」；
   - 用户点击「开始试跑」，按钮展示 Loading 并禁用文件更换；
   - 接口返回后，将原始图片渲染于自适应容器中，其上覆盖透明的 HTML5 Canvas；
   - 右侧或下方展示统计摘要：推理耗时、检出目标数、类别汇总；
   - 提供「下载带框图」按钮。

### 3.2 坐标系与高分辨率 (HiDPI) 绘制
1. **归一化映射**：
   - 算法输出 `box: [x1, y1, x2, y2] ∈ [0.0, 1.0]`；
   - 实际像素坐标：`X1 = x1 * canvas.width`, `Y1 = y1 * canvas.height`, 宽 = `(x2 - x1) * canvas.width`, 高 = `(y2 - y1) * canvas.height`。
2. **清晰度保障**：Canvas 内部物理像素根据容器实际尺寸与 `window.devicePixelRatio` 动态同步，避免 Retina 屏幕模糊。
3. **分层绘制样式**：
   - **通用检测**：`#3b82f6`（蓝色框），顶部绘制半透明标签底色条，显示 `{label} {(score * 100).toFixed(1)}%`；
   - **人体/人脸**：
     - 人体全身框：`#10b981`（绿色框）；
     - 人脸框：`#f59e0b`（橙色框），关键点绘制小圆点；
   - **车辆/车牌**：
     - 车辆框：`#10b981`（绿色框）；
     - 车牌框：`#06b6d4`（青色框），标注车牌号与颜色；
   - **分类算法**：Canvas 不画框，在界面侧边以 Badge 列表展示各类别的置信度。

---

## 4. 资源控制与安全防范

1. **瞬态存储生命周期**：
   - Go 侧使用 `os.CreateTemp("", "zhulong_trial_*.ppm")` 生成临时文件，在调用完成后通过 `defer os.Remove(...)` 确保 100% 清理；
   - 绝不向 SQLite 数据库写任何记录；
   - 绝不调用媒体录制与抓拍服务。
2. **硬件互斥与争用保护**：
   - 严格限定仅在 `DISABLED` 状态下允许试跑，防止多个常驻 Worker 进程与试跑子进程争夺 RKNN/NPU 资源或造成显存溢出；
   - 每次试跑执行受 15 秒 `context.WithTimeout` 保护，超时强制 kill 子进程，杜绝僵尸进程。
