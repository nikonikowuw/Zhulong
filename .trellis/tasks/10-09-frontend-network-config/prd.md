# 系统设置网络配置UI与两阶段安全回滚交互 · PRD

## 1. 目标 (Goal)

在 Web 前端「系统设置」模块中实现工业级网络配置界面（`/settings/network`），完整呈现多物理网卡实时状态（载波检测、IP/掩码/网关/DNS、当前进站网卡标识、默认路由标记），支持 DHCP / 静态 IP 切换配置与网络连通性探测（Ping），并深度协同后端的两阶段看门狗回滚机制（Watchdog Transaction），保障边缘设备在修改网络参数时具备防失联倒计时确认与新地址迁移引导能力。

---

## 2. 需求列表 (Requirements)

### 2.1 网卡状态看板与列表展示 (Interface Dashboard)
- **多网卡概览**：调用 `GET /api/v1/system/network/interfaces` 获取物理网卡列表，支持骨架屏（Loading）、空数据（Empty）与错误重试（Error）三态。
- **接口详情指标**：
  - 物理接口名（如 `eth0`, `eth1`）与 MAC 地址；
  - 物理链路载波状态（`linkUp`）：使用鲜明徽章（绿色 Link Up / 灰色 Disconnected）；
  - 获取方式（`mode`）：DHCP 自动分配 vs Static 静态指定；
  - IPv4 地址列表（CIDR 格式与拆分呈现）；
  - 默认网关（Gateway）与 DNS 服务器列表；
  - 核心状态标识：
    - **默认网关标识**（`isDefaultGw`）：高亮徽章标注承载全局默认路由；
    - **当前进站网卡警示**（`isCurrent`）：特别标注用户当前 Web 请求通过该网卡进站，明确提示配置修改可能导致网络短暂中断或地址迁移。
- **连通性探测 (Ping Probe)**：
  - 提供轻量 Ping 探测弹窗/工具，调用 `POST /api/v1/system/network/ping`，支持输入网关或目标 IP，展示连通性结果（Reachable / Unreachable）与 RTT 延迟毫秒数。

### 2.2 网卡配置表单与交互 (Configuration Dialog & Zod Form)
- **编辑入口**：网卡卡片提供「配置网络」操作按钮，唤出配置模态框或抽屉。
- **模式切换**：支持 DHCP 与 Static 模式切换。
  - 选择 DHCP 模式时：静态 IP、掩码、网关输入框置灰禁用或隐藏，仅允许指定 DNS（可选）与设为默认网关选项；
  - 选择 Static 模式时：展开必填输入项（IP 地址、子网掩码），选填项（网关、DNS 列表）。
- **默认路由互斥提示**：支持「设为默认网关 (`setDefault`)」复选框。若勾选且系统已有其他默认网关，提示将切换全局路由出口。
- **输入校验 (Zod Schema Validation)**：
  - IPv4 格式合法性校验（点分十进制 0-255 范围）；
  - 子网掩码合法性校验（255.255.255.0、255.255.0.0 等有效连续子网掩码）；
  - 网关与 DNS 列表的有效 IP 格式校验。

### 2.3 两阶段安全回滚与迁移机制 (Two-Phase Watchdog & Safe Rollback)
- **提交试运行**：
  - 提交配置调用 `POST /api/v1/system/network/interfaces/:name/apply`；
  - 后端返回 `ApplyResponse`，包含 `transactionId`、`timeoutSec`（默认 60s）、`targetUrl` 与 `confirmToken`。
- **倒计时安全确认模态框 (Safe Confirmation Modal)**：
  - 提交成功后立即唤起强制确认模态框，启动 60 秒高精度倒计时进度条；
  - **原地确认场景**（IP 未变或在同子网）：
    - 提供【确认生效】按钮，点击调用 `POST /api/v1/system/network/confirm?token=...`，固化配置并解除看门狗；
    - 提供【立即回滚】按钮，点击调用 `POST /api/v1/system/network/rollback?token=...`，立即恢复原配置。
  - **地址迁移场景**（当前网卡 IP 变更，`targetUrl` 变动）：
    - 模态框高亮警示：“设备 IP 已变更，当前连接即将中断！”
    - 显示新访问地址，并提供【跳转到新地址完成确认】快捷按钮（新页面携带 Token 访问 `/settings/network?confirm_token=...` 自动唤醒确认）。
- **事务状态感知与恢复 (Transaction Recovery)**：
  - 页面初次加载时调用 `GET /api/v1/system/network/status`；
  - 若系统正处于 `pending_confirm` 状态，自动恢复倒计时确认条，避免管理员刷新页面后失去确认入口导致意外回滚。

### 2.4 导航与路由集成 (Routing & Nav)
- 在 TanStack Router 中注册 `src/routes/_authenticated/settings/network.tsx`；
- 在 `src/features/settings/index.tsx` 的侧边导航栏及 `src/components/layout/data/sidebar-data.ts` 的设置子菜单中增加「网络设置 (Network)」导航项。

---

## 3. 验收标准 (Acceptance Criteria)

- [x] **路由与菜单**：访问 `/settings/network` 能够正常加载网络配置页面，设置导航与侧边栏高亮正确。
- [x] **网卡列表看板**：正常展示物理网卡、MAC、LinkUp、IP 地址、网关、DNS、默认网关与当前通信网卡标识；支持刷新与错误处理。
- [x] **连通性探测**：能够成功调用 `/system/network/ping` 对目标地址进行测试并直观反馈延迟与可达性。
- [x] **表单校验与提交**：静态模式下对 IPv4 与子网掩码进行严格校验；DHCP 模式正确禁用静态字段；提交前进行二次风险确认（修改当前进站网卡时提示失联风险）。
- [x] **两阶段回滚交互**：调用 `/apply` 后正确展示倒计时；支持在倒计时内调用 `/confirm` 确认固化或调用 `/rollback` 恢复。
- [x] **新地址迁移引导**：当修改导致目标 URL 改变时，界面明确提供新地址引导跳转链接。
- [x] **未决事务恢复**：刷新页面能通过 `GET /system/network/status` 识别活跃事务并维持确认状态。
- [x] **质量验证**：代码符合零 `any`、TanStack Query 统一管理服务端状态、测试通过且 Lint 0 警告。
