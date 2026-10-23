# Zhulong 系统总体架构规范

> 系统拓扑、单主程序交付模型、当前实现边界与跨层责任。硬件能力和详细行为以对应层规范为准。

---

## 1. 系统拓扑

```txt
┌──────────────────────────────────────────────────────────────────────┐
│ React 19 SPA · TypeScript · Vite · TanStack · shadcn/ui             │
│ 英文 / 简体中文 / 繁体中文 · 主题与布局                                │
└───────────────────────────────▲──────────────────────────────────────┘
                                │ HTTP / SSE / WebSocket
┌───────────────────────────────▼──────────────────────────────────────┐
│ Go Host · Gin · Uber Fx · SQLite                                     │
│ 业务域、认证、配置、调度、API 与静态资源                               │
└───────────────────────────────▲──────────────────────────────────────┘
                                │ CGO · 纯 C ABI
┌───────────────────────────────▼──────────────────────────────────────┐
│ C++ Engine · FFmpeg/RTSP · capture/decode · FrameBroker · snapshot    │
│ 可选 Runner 推理与 RGA/RKNN 后端；有界队列与显式资源所有权              │
└───────────────────────────────▲──────────────────────────────────────┘
                                │ Linux UAPI · vendor runtime · DMA-BUF
┌───────────────────────────────▼──────────────────────────────────────┐
│ IP Camera · Linux media devices · platform-specific accelerators      │
└──────────────────────────────────────────────────────────────────────┘
```

**DMA-BUF 是按平台、格式和后端条件启用的路径，不是所有帧都零拷贝。** CPU/共享内存回退、颜色转换、预处理或布局变化都可能产生拷贝；只有已验证的具体数据路径才可称为零拷贝。见 [原生硬件后端规范](./native/hardware-backends.md) 与 [算法插件契约](./native/algo-plugin-contract.md)。

---

## 2. 交付模型

1. **单宿主程序**：Go 构建为一个可执行文件；运行时不依赖 Node.js。
2. **内嵌前端**：Vite 产物通过 `//go:embed` 打入 Go 程序，发布前同步 `internal/webui/dist`。
3. **原生 ABI 边界**：C++ Engine 通过 `native/include/Zhulong/engine.h` 的纯 C ABI 静态链接；Go 侧仅由 `internal/engine` 使用 CGO。C++ 异常不得穿越 ABI。
4. **外部运行库**：允许依赖宿主 C 运行库、线程库和所选平台的厂商运行库；不得假定宿主二进制是完全静态链接。
5. **持久化分离**：SQLite、算法包和媒体文件位于配置的数据/媒体目录；迁移与文件生命周期分别由数据库和 storage 域管理。

---

## 3. 当前实现边界

下表是能力摘要，不替代具体 API、ABI、线程、生命周期和平台合同。修改能力时，应同时核对对应规范与实现/测试。

| 能力 | 当前边界 | 详细规范 |
| --- | --- | --- |
| Go Host | Fx 生命周期、版本化 SQLite 迁移、认证、摄像机、人员、算法 Runner、storage、audit、task、record 等业务域已实现。 | [Backend index](./backend/index.md) |
| 媒体 Engine | C ABI 提供流探测、获取/释放、订阅、FrameBroker 和抓拍能力；原生实现包含 RTSP 接入与解码。不是单纯的生命周期 stub。 | [接入合同](./native/ingestion-contract.md)、[插件与 FrameBroker](./native/algo-plugin-contract.md) |
| 算法与底库 | Runner 支持启动时全量同步及运行期增量同步；推理后端由构建 profile 选择。 | [插件契约](./native/algo-plugin-contract.md)、[硬件后端](./native/hardware-backends.md) |
| DMA-BUF | 解码帧可经 FrameBroker 传递 DMA-BUF FD；支持路径依赖驱动、格式、设备权限与后端组合。CPU/共享内存路径仍可能复制，host 构建或单元测试不证明目标板上的硬件路径可用。 | [硬件后端](./native/hardware-backends.md)、[插件与 IPC](./native/algo-plugin-contract.md) |
| 感知记录与抓拍 | `StreamHub` 在推理结果 fan-out 前应用区域过滤；record 域负责识别/告警/目标抓拍记录和异步证据图。它不等同于连续视频录像。 | [感知记录与抓拍](./backend/record-guidelines.md)、[摄像机规范](./backend/camera-guidelines.md) |
| 连续录像 | 当前仓库有 recordings 的存储、保留与读取边界，但未交付持续写入视频文件的 recorder 数据面。 | [Storage](./backend/storage-guidelines.md)、[Media file](./backend/mediafile-guidelines.md) |

平台兼容性、量化类型、驱动版本与实测数据必须引用具体 profile、SDK/BSP 和验证条件。主机测试通过不代表目标板硬件路径已验证。

---

## 4. 跨层责任边界

| 边界 | 合法职责 | 禁止事项 |
| --- | --- | --- |
| **React ⇄ Go** | API 使用统一 JSON 信封；实时业务按契约使用 SSE/WebSocket；前端解析不可信输入。 | 暴露原生指针、底层驱动错误或未脱敏凭据；API 错误回退成 SPA HTML。 |
| **Go ⇄ CGO** | 仅经公开 C ABI；opaque handle 与状态码；明确同步回调和缓冲区生命周期。 | 把含 Go 指针的内存交给 C；在调用返回后保留未 pin 的 Go 指针；依赖 GC 释放原生资源。 |
| **C ABI ⇄ C++** | 在每个导出边界捕获异常并转成状态码；公开 ABI 保持纯 C。 | C++ 异常穿越 ABI；公开 C 头文件暴露 C++ ABI。 |
| **C++ ⇄ Driver/NPU** | 按平台能力使用媒体 UAPI、厂商 runtime 与 DMA-BUF；队列容量、FD 所有权和同步语义必须明确。 | 把可选硬件路径描述成通用保证；无界缓存；跨线程无保护访问硬件上下文。 |

---

## 5. 已确定的关键选型

- C++17 与 CMake 管理原生静态 Engine；根级 `Makefile` 编排前端、Native、Swagger 与 Go 构建。
- Go 通过 CGO `mattn/go-sqlite3` 访问 SQLite，schema 变更使用版本化 SQL migration，不用 GORM `AutoMigrate`。
- 前端使用 `react-i18next` / `i18next`，语言为 `en`、`zh-Hans`、`zh-Hant`。
- 硬件后端是否可用由目标平台 profile 与 SDK/BSP 决定；参阅 [原生硬件后端规范](./native/hardware-backends.md)，不要从通用架构图推断支持范围。
