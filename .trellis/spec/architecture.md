# Zhulong 系统总体架构规范

> Zhulong 系统全局拓扑、方案 A 部署模型、跨层责任边界及技术选型规则。

---

## 1. 系统拓扑与总体架构

```txt
┌────────────────────────────────────────────────────────────────────────┐
│                        React 前端界面 (Web SPA)                         │
│  React 18+, TypeScript (严格模式), Vite, TanStack Query, shadcn/ui     │
│  深浅双色主题, 英文/简体中文/繁体中文 全文国际化                         │
└───────────────────────────────────▲────────────────────────────────────┘
                                    │ HTTP (Gin) / SSE (推理事件) / WebSocket
┌───────────────────────────────────▼────────────────────────────────────┐
│                        Go 业务应用宿主 (Host)                           │
│  业务模块分包 (cmd/Zhulong, internal/{camera,person,algorithm,...})       │
│  Uber Fx 生命周期编排 (OnStart/OnStop), GORM + SQLite, Swaggo 2.0      │
│  统一响应信封: { code: "OK", message, data, details? }                │
└───────────────────────────────────▲────────────────────────────────────┘
                                    │ CGO / C ABI 门面 (include/Zhulong/engine.h)
┌───────────────────────────────────▼────────────────────────────────────┐
│                      原生处理引擎 (C++ Pipeline)                        │
│  处理节点流水线 (Node Pipeline: 采集 ➔ 解码 ➔ 预处理 ➔ 推理)            │
│  有界队列背压, 零拷贝缓冲传递, 严格的所有权生命周期                      │
└───────────────────────────────────▲────────────────────────────────────┘
                                    │ Linux UAPI / 硬件驱动动态库
┌───────────────────────────────────▼────────────────────────────────────┐
│                       底层硬件与外设交互加速                            │
│  IP 摄像机 (RTSP/ONVIF), 硬件编解码, 图像处理, 异构 NPU/GPU, Linux UAPI│
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 方案 A 单主程序交付模型

1. **单宿主可执行程序**：Go 编译为单个应用二进制（如 `Zhulong`）。
2. **内嵌前端静态资源**：Vite 构建产物通过 `//go:embed` 打包至 `internal/webui`，由 Go 直接托管，零 Node.js 运行时依赖。
3. **原生 C++ 内联链接**：当前仅交付无硬件生命周期 stub，通过 C ABI 静态链接进主程序；未来媒体流水线另行实现。
4. **允许外部动态链接**：允许动态依赖宿主 Linux 的 C 运行时（glibc/musl）、线程库及未来选择的厂商驱动（非 all-static）。
5. **外部持久化存储分离**：当前本机 host 将 SQLite 存于 `<data-dir>/zhulong.db`，默认目录为 `./data`，可由 `config.toml` 中 `[data].directory` 覆盖；未来录像文件与模型权重另行放入配置的数据卷。

## 当前实现边界

本仓库已完成 `cmd/Zhulong`、`internal/app`、`database`（`000001`–`000007` 版本化迁移）、`engine`、`httputil`、`webui`、`auth` 单用户凭据会话、`camera` 全生命周期管理、`person` 人员与人脸底库域、`algorithm` 算法包生命周期与 Runner 进程监督、`storage` 存储水位感知、`network` 网络配置两阶段回滚看门狗、`systemtime` 时间同步与 `audit` 操作审计。

**尚未交付**：人员底库经 UDS 下发至 Runner 的热更新链路、算法推理结果的落库与事件派发、录像模块——不得把架构目标图解读为已全量交付。当前 native 组件已支持宿主 C ABI、流探测、按需包借用与订阅生命周期，以及 FrameBroker 共享内存帧分发宿主（每路 AI 流一个 broker，发布侧按 `max(sample_fps)` 节流，socket 路径经 C ABI 交给 Go）与 runner 多路 `--broker` 连接。⚠️ 该通路走的是**逐 plane 拷贝到共享内存**的参考路径，**不是** DMA-BUF 零拷贝；帧数据不经过 Go，但「零拷贝」不能据此宣称已交付。

**RKNN 推理后端与 RGA 视觉前处理后端均已交付**（详见 [原生硬件加速后端规范](./native/hardware-backends.md)）：

- `native/src/backends/rknn/` 实现 `IInferenceBackend` 的 RKNN 版本，经**编译期 CMake profile** 选择（`ZHULONG_INFERENCE_BACKEND=cpu|rknn`），`rknn_dup_context` 权重共享已在 RK3568 真机验证成立（4 上下文 RSS 峰值 50.4MB → 14.3MB）。
- `native/src/backends/rga/` 实现 `IVisionBackend` 的 RGA 版本，经**编译期 CMake profile** 选择（`ZHULONG_VISION_BACKEND=cpu|rga`），支持 Letterbox、Resize、两阶段 Crop & Scale 及 NV12/YUV420P 多平面硬件 CSC 转换，并在不可用时具备透明 CPU 回退机制。
⚠️ 两个硬件后端均**接受 host↔device 的拷贝**，同样**不是**零拷贝；DMA-BUF 零拷贝数据通路仍**未交付**。

---

## 3. 跨层交互防爆门契约

| 跨层边界 | 允许的交互方式 | 绝对严禁的反模式 |
| --- | --- | --- |
| **React ⇄ Go** | Gin 处理标准 JSON、SSE 推送实时检测、WebSocket 设备遥测；中间件流水线 (Recovery/RequestID/AccessLog) 保障隔离与追踪 | 暴露底层指针地址；裸漏 SQLite 报错或 CGO 堆栈；全局滥挂 API 中间件污染 SPA 静态文件 |
| **Go ⇄ CGO / C ABI** | 仅经 `include/Zhulong/engine.h` 交互；使用不透明句柄与基本类型 | 跨 CGO 传含 Go 指针的内存；C 长期持有未 Pinned 的 Go 指针；漏调 `C.free` |
| **C ABI ⇄ C++** | 在 `src/abi/` 内以 `try-catch (...)` 拦截所有异常并转为状态码 | C++ 异常穿越 C ABI；公开头文件出现 C++ class/STL/模板 |
| **C++ ⇄ 驱动/NPU** | DMA-BUF 零拷贝流转、V4L2 抓帧、硬件加速上下文 | 网络断流或推理拥塞时无界堆积内存；多线程无保护竞态访问硬件上下文 |

---

## 4. 延迟决定的技术选型 (Deferred Selections)

- **CMake 构建系统**：已选用 CMake 管理 C++17 静态库；根级 Makefile 编排前端、native、Swagger 和 Go build 顺序。
- **SQLite 驱动**：使用 CGO `mattn/go-sqlite3`，GORM SQLite driver 用于业务连接，`golang-migrate` sqlite3 adapter 用于版本化 migrations。
- **前端 i18n**：使用 `react-i18next` / `i18next`，语言选项为 `en`、`zh-Hans`、`zh-Hant`；硬件与部署目标仍待实机选定。
