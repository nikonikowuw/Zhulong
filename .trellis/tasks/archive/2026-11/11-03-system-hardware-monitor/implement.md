# 系统硬件信息与异构资源监控实施计划 (Implementation Plan)

## 阶段一：后端开发与单元测试

1. **新建 `internal/systeminfo/` 模块**：
   - `model.go`：定义所有 Response、Info、Status 结构体。
   - `ring_buffer.go`：实现固定长度的硬件时序环形缓冲区 `RingBuffer` 及单元测试。
   - `collector.go`：定义 `Collector` 接口与公用解析函数。
   - `collector_linux.go`：Linux 专用读取器（`/proc/stat`, `/proc/meminfo`, `/proc/net/dev`, `/proc/mounts`, `/sys/class/thermal`, `/sys/kernel/debug/rknpu/load` 等）。
   - `collector_fallback.go`：非 Linux 环境下的降级模拟读取器。
   - `service.go`：管理采集生命周期（定时采样，存入 RingBuffer，计算 ΔRate，提供查询）。
   - `handler.go`：Gin API Handler，挂载 `GET /api/v1/system/hardware`。
   - `module.go`：Fx 依赖注入装配。
2. **测试与覆盖**：
   - `ring_buffer_test.go`：测试环形缓冲区的插入、溢出覆盖、快照提取。
   - `collector_test.go`：mock procfs / sysfs 字符串解析测试（CPU 算式、内存换算、网络速率、NPU 正则）。
   - `service_test.go`：Service 采样与并发安全测试。
   - `handler_test.go`：HTTP 接口与错误处理测试。
3. **集成到 `internal/app` 与 `Makefile`**：
   - 在 `internal/app/app.go` 中注册 `systeminfoModule`，将路由挂载至 `protected_routes`。
   - 在 `Makefile` 的 `api-docs` 白名单追加 `internal/systeminfo`。
   - 运行 `make api-docs` 生成 Swagger 文档并更新。

## 阶段二：前端功能开发与多语言支持

1. **新建 `web/src/features/settings/system/` 模块**：
   - `types.ts`：Zod Schema & 类型导出。
   - `api/system-api.ts`：TanStack Query Hook `useSystemHardware`，设置 `refetchInterval: 2500`。
   - `components/`：
     - `system-header.tsx`：设备概览与刷新控制。
     - `stat-cards.tsx`：CPU、内存、NPU、网络、主磁盘状态卡片。
     - `trend-charts.tsx`：Recharts 平滑走势图（CPU/NPU, 内存, 网络吞吐, 温度）。
     - `storage-table.tsx`：挂载存储卷列表。
     - `network-table.tsx`：网卡流量明细。
     - `thermal-cards.tsx`：各芯片温度监测点。
   - `locales/`：英文、简体中文、繁体中文语言文件。
2. **路由挂载与导航集成**：
   - 新建 `web/src/routes/_authenticated/settings/system.tsx`。
   - 更新 `web/src/features/settings/index.tsx` 侧边栏，增加 `System` 项。
   - 更新 `web/src/components/layout/data/sidebar-data.ts`，在 Settings 下增加 `System` 项。
   - 更新 `web/src/locales/` 导航文案。

## 阶段三：全面验证与门禁测试

1. 运行 `make api-docs` 确保文档生成正确。
2. 运行 `python3 native/scripts/build.py go test -race ./internal/systeminfo/...` 测试后端竞态与逻辑。
3. 运行 `make go-check` 验证后端整体构建与 vet。
4. 运行 `cd web && pnpm lint && pnpm type-check && pnpm test` 前端测试。
5. 运行 `make check` 全项目门禁。
