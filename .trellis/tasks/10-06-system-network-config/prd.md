# 边缘异构系统网络配置与两阶段安全回滚 (System Network Configuration)

## Goal

为面向极端边缘异构 Linux 设备（如 RK3588、NVIDIA Jetson、华为昇腾及通用工控机）的烛龙（Zhulong）单应用服务，提供工业级宿主机网络配置管理能力。
支持主流网络管理栈自适应探测、防掉电两阶段回滚看门狗、跨 IP 快捷免密确认、单默认网关约束以及极端环境下的 CLI 紧急救援，保障设备在无外网、无显示器、纯现场网线直连环境下永不“变砖”失联。

## Scope

本次任务采用**全链路端到端交付（前后端 + 系统适配）**：
1. **Go 后端与系统适配 (`internal/network`)**：
   - 跨平台内核态网卡状态与拓扑采集（基于 Linux Netlink / `sysfs`，过滤虚拟网卡，标记当前访问接口 `is_current`）。
   - 异构 Linux 网络栈自适应下发适配器（自动探测 NetworkManager、`systemd-networkd`，支持自定义 Hook 脚本兜底）。
   - 防掉电两阶段回滚看门狗事务状态机（落盘持久化 `<data-dir>/network_transaction.json`，超时回滚，`app.OnStart` 开机未确认自愈回滚）。
   - 跨 IP 迁移协议与一次性快捷确认令牌（`confirm_token`），支持免密确认与管理员会话确认双通道。
   - 极端环境 CLI 紧急救援支持（`Zhulong --reset-network` 一键恢复出厂维护 IP `192.168.1.168/24`）。
   - Web 连通性轻量探测接口（ICMP Ping / 网关探测）。
   - RESTful API 挂载与 Swagger 2.0 文档注解，集成至 Uber Fx 依赖注入生命周期。
2. **React 前端交互 (`web/src/features/systemSettings` / `features/network`)**：
   - 全局信息架构集成：在 `Sidebar.tsx` 新增「系统设置」（`#settings`）一级入口，二级导航首期展示「网络配置」。
   - 网卡状态与拓扑看板：卡片式呈现物理网卡列表、MAC、Link Up/Down、实时 IP、当前会话高亮徽标。
   - 配置与校验表单：支持 DHCP 与静态 IP 切换，IP/掩码/网关/DNS 实时客户端格式校验，单默认网关冲突互斥约束。
   - 两阶段安全应用模态框：切 IP 倒计时提示、新地址跳转指引、悬浮回滚倒计时横幅。
   - 全文国际化（英文 `en`、简体中文 `zh-Hans`、繁体中文 `zh-Hant`）。

## Target Scenarios

1. **现场工程人员直连初调**：工程人员笔记本电脑通过网线直连边缘盒子出厂维护网口（`192.168.1.168`），在 Web 控制台中将网口变更为业主局域网静态 IP。
2. **配置误操作与防失联保护**：现场人员误填错误网关或掩码导致网络中断；无需任何人工介入，60 秒倒计时结束后设备自动还原上一版本正常配置。
3. **现场异常掉电自愈**：在两阶段试运行倒计时期间，现场人员误拔电源或设备断电；重启后开机自检自动识别未确认事务并强制回退至安全网络，杜绝开机变砖。
4. **串口/本地终端极端救援**：在极端网络灾难导致 Web 完全无法访问时，售后工程师通过串口或终端执行 `Zhulong --reset-network` 即可恢复维护网口。

## Requirements

### 1. 物理网卡拓扑与当前会话感知
- **网卡白名单过滤**：严格通过 `/sys/class/net/<iface>/device` 或 `ARPHRD_ETHER` 判定物理硬件，严格隐藏 `lo`（环回）、`docker*`、`br-*`（虚拟网桥）、`tun*`、`veth*` 等虚拟接口。
- **当前接口标记 (`is_current`)**：结合请求入站的 Local Address 与本地网卡子网，判定当前 HTTP 连接所走的物理网卡，打上 `is_current: true` 标记。
- **单默认网关约束**：全局仅允许一个网卡启用并配置默认网关（Default Gateway，`0.0.0.0/0`），防止多网卡配置多网关造成 Linux 路由抖动或黑洞。

### 2. 异构网络驱动自适应下发引擎
- 核心定义 `NetworkProvider` 接口：
  - `ListInterfaces(ctx) ([]InterfaceInfo, error)`
  - `ApplyConfig(ctx, cfg InterfaceConfig) error`
  - `RollbackConfig(ctx, backup BackupConfig) error`
  - `ResetToDefault(ctx) error`
- **自适应探测链**：
  1. **NetworkManager Provider**：探测系统 D-Bus 或 `nmcli`，通过连接配置文件应用并持久化。
  2. **systemd-networkd Provider**：探测 `/run/systemd/system`，写入 `/etc/systemd/network/10-<iface>.network` 并执行 `networkctl reload`。
  3. **Custom Script Provider**：若配置文件指定脚本路径，优先委托外部 hook 脚本执行。
- **运行时状态读取**：所有 Linux 平台均通过通用 Netlink / `sysfs` 读取，零发行版差异。

### 3. 两阶段回滚看门狗与防掉电事务状态机
- **事务文件持久化**：
  - 路径：`<data-dir>/network_transaction.json`。
  - 写入时机：在应用新配置前落盘并 `fsync`，包含状态 `status: "pending_confirm"`、倒计时过期时间戳、备份配置 `rollback_config`。
- **时序与生效**：
  - 收到 Apply 请求后，先完整返回 HTTP 200 响应（包含 `transaction_id`, `timeout: 60`, `target_url`, `confirm_token`），协程延时 300ms 后触发底层网络切换，确保前端必定收到响应。
- **确认与固化**：
  - 支持免密通道：`POST /api/v1/system/network/confirm?token=xxx`（一次性高熵令牌，60 秒过期，用后即毁）。
  - 支持会话通道：已登录管理员在过期前调用确认接口。
  - 确认成功后删除事务文件，固化配置为 `last_known_good`。
- **超时与开机自愈**：
  - 内存定时器 60 秒到期未确认触发回滚。
  - `app.OnStart` 系统启动阶段检测到 `network_transaction.json` 处于未确认状态，立即无条件执行回退并记录警示日志。

### 4. 极端自救 CLI 与 Web 诊断
- **命令行紧急重置**：
  - 支持 `Zhulong --reset-network` 参数，在不启动主 Web 服务的情况下执行硬件网卡重置（`eth0` 强制配置为 `192.168.1.168/24` 维护静态 IP，其余物理网卡恢复 DHCP），重置完成后退出。
- **连通性探测接口**：
  - `POST /api/v1/system/network/ping`：对指定网关或 IP 执行轻量探测（发包 2 次，超时 1.5s），返回时延与连通结果。

### 5. 权限管理与运行态约束
- **权限预检**：
  - 裸机环境下需要 `CAP_NET_ADMIN`（或 root）。
  - 后端初始化时执行权能预检；若无权管理网络，相关修改接口返回标准错误码 `SYSTEM_NETWORK_PERMISSION_DENIED`，界面呈现只读与授权告警。

### 6. 前端界面与交互设计
- **导航集成**：侧边栏 `Sidebar.tsx` 新增「系统设置」项，对应路由 `#settings`。
- **设置看板**：
  - 顶部子导航（首期：网络设置）。
  - 网卡列表卡片：展示网卡名称、物理 MAC、当前状态（已连接/未连接）、IP/掩码/网关/DNS、当前管理网口徽标。
  - 编辑表单模态框：支持 DHCP 与静态 IP 单选，输入框合法性校验，默认网关唯一性互斥提示，提供「测试连通性」按钮。
  - 迁移引导模态框：当修改当前管理网口时，弹窗警示并展示 60 秒倒计时进度条、新访问地址超链接与一键跳转按钮。
- **国际化**：英文、简体中文、繁体中文三种语言包完整对齐。

## Acceptance Criteria

- [ ] **物理网卡精确过滤与感知**：API 仅返回真实物理网卡，虚拟网卡（lo、docker0、br-* 等）全部被过滤；准确识别请求当前进站网卡并置 `is_current: true`。
- [ ] **单默认网关冲突校验**：后端与前端均禁止为多个网卡同时开启/配置默认网关，配置冲突时给出明确拒绝原因。
- [ ] **自适应下发能力**：在具备 NetworkManager 或 `systemd-networkd` 的 Linux 环境下均可成功配置静态 IP 与 DHCP，配置能跨系统重启持久化。
- [ ] **两阶段倒计时回滚**：下发新 IP 后若 60 秒内未收到 Confirm 请求，系统自动回滚至上一版本有效网络。
- [ ] **防拔电源掉电自愈**：在 pending 期间切断进程或重启，开机启动自检时能准确捕获未确认事务并强制恢复旧 IP，不出现网络死锁。
- [ ] **一次性凭证快捷确认**：在新 IP 页面未登录态下，携带有效的 `confirm_token` 能成功固化网络配置。
- [ ] **CLI 紧急恢复**：运行 `Zhulong --reset-network` 能将主物理网口重置为 `192.168.1.168/24` 维护模式。
- [ ] **前端系统设置体验**：侧边栏可进入系统设置网络面板，三种语言切换流畅无硬编码，表单校验与迁移弹窗倒计时体验良好。
- [ ] **测试覆盖率**：包含状态机、IP 校验、网卡解析、API Handler 以及前端组件的完整单元测试。
