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
│  业务模块分包 (cmd/Zhulong, internal/{camera,recording,inference,...})    │
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
5. **外部持久化存储分离**：当前本机 host 将 SQLite 存于 `<data-dir>/zhulong.db`，默认目录为 `os.UserConfigDir()/Zhulong`，可由 `--data-dir` 覆盖；未来录像文件与模型权重另行放入配置的数据卷。

## 当前实现边界

本仓库已完成 `cmd/Zhulong`、`internal/app`、`database`、`engine`、`httputil`、`webui`、`auth` 单用户凭据会话以及 `camera` 全生命周期管理。录像、推理模块以及 C++ 解码/预处理流水线仍属后续目标，不得把架构目标图解读为已全量交付。当前 native 组件已支持宿主 C ABI、流探测、按需包借用与订阅生命周期。

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
