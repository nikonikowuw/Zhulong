# 边缘异构系统网络配置与两阶段安全回滚 · 技术架构设计 (Technical Design)

## 1. 架构定位与系统边界

网络配置模块（`internal/network`）属于宿主机基础设施管理组件，向下对接 Linux 内核网络协议栈（Netlink / `sysfs`）及宿主网络服务守护进程（NetworkManager / `systemd-networkd`），向上通过 Gin 暴露受保护的 RESTful API，最终由 React 前端「系统设置」模块呈现。

```txt
┌──────────────────────────────────────────────────────────────────┐
│              React 前端界面 (Web SPA - #settings)                 │
│   NetworkCard 网卡看板 ⇄ NetworkEditModal ⇄ NetworkMigrationModal  │
└─────────────────────────────────▲────────────────────────────────┘
                                  │ HTTP API (REST / JSON Envelope)
┌─────────────────────────────────▼────────────────────────────────┐
│                   Go 业务层 (internal/network)                    │
│  ┌───────────────────────┐      ┌─────────────────────────────┐  │
│  │    NetworkService     │◄────►│  WatchdogTransactionManager │  │
│  └───────────┬───────────┘      └──────────────┬──────────────┘  │
│              │                                 │ 持久化状态机落盘  │
│              ▼                                 ▼                 │
│      NetworkProvider (接口)       <data-dir>/network_transaction.json
│  ┌───────────────────────────────────────────┐                   │
│  │ AutoDetectProvider (自动嗅探与分发)       │                   │
│  │ ├─ NetworkManagerAdapter (nmcli / D-Bus)  │                   │
│  │ ├─ SystemdNetworkdAdapter (.network 文件) │                   │
│  │ └─ CustomScriptAdapter   (Hook 脚本执行)  │                   │
│  └───────────────────────────────────────────┘                   │
└─────────────────────────────────▲────────────────────────────────┘
                                  │ RTNETLINK 套接字 / sysfs / ioctl
┌─────────────────────────────────▼────────────────────────────────┐
│               Linux 内核与硬件物理接口 (Host Kernel)              │
│       eth0 (有线/专网)    eth1 (管理/上行)    wlan0 (无线)        │
└──────────────────────────────────────────────────────────────────┘
```

---

## 2. 后端核心抽象与领域模型 (`internal/network`)

### 2.1 目录组织
```txt
internal/network/
├── model.go             # 领域模型定义（网卡信息、配置负载、事务结构）
├── provider.go          # NetworkProvider 抽象接口与探测器
├── provider_nm.go       # NetworkManager 适配器实现
├── provider_systemd.go  # systemd-networkd 适配器实现
├── provider_script.go   # 外部 Hook 脚本适配器实现
├── reader_linux.go      # 基于 Netlink/sysfs 的通用物理网卡状态读取
├── reader_fallback.go   # 非 Linux 环境编译兼容桩 (Darwin/Windows)
├── watchdog.go          # 两阶段看门狗与防掉电落盘状态机
├── service.go           # 核心业务编排（冲突校验、单网关控制、Token 派发）
├── handler.go           # Gin 路由处理与 Swagger 2.0 注解
├── handler_test.go      # API 单元与契约测试
├── ping.go              # 轻量网关连通性探测器
└── module.go            # Uber Fx 模块注入定义
```

### 2.2 数据模型定义 (`model.go`)
```go
// InterfaceInfo 描述物理网卡的实时运行状态
type InterfaceInfo struct {
    Name        string   `json:"name"`         // 接口名，例如 "eth0"
    MAC         string   `json:"mac"`          // 物理 MAC 地址
    LinkUp      bool     `json:"link_up"`      // 物理载波 Link 状态 (网线是否插入)
    Mode        string   `json:"mode"`         // "dhcp" 或 "static"
    IPAddresses []string `json:"ip_addresses"` // 当前生效的 IPv4 CIDR 列表
    Gateway     string   `json:"gateway"`      // 当前网卡配置的网关（可能为空）
    DNS         []string `json:"dns"`          // DNS 服务器列表
    IsDefaultGW bool     `json:"is_default_gw"`// 是否承载全局默认路由 (0.0.0.0/0)
    IsCurrent   bool     `json:"is_current"`   // 是否为当前 HTTP 请求进站网卡
}

// InterfaceConfig 用于提交网卡配置修改
type InterfaceConfig struct {
    Mode       string   `json:"mode" binding:"required,oneof=dhcp static"` // dhcp / static
    IPAddress  string   `json:"ip_address"`                                // 静态 IP, 如 "192.168.1.100"
    SubnetMask string   `json:"subnet_mask"`                              // 子网掩码, 如 "255.255.255.0"
    Gateway    string   `json:"gateway"`                                  // 默认网关（可选）
    DNS        []string `json:"dns"`                                      // DNS 服务器列表
    SetDefault bool     `json:"set_default"`                              // 是否指定为默认网关
}

// TransactionState 描述正在进行的网络两阶段事务
type TransactionState struct {
    TransactionID string          `json:"transaction_id"`
    Status        string          `json:"status"` // "idle" | "pending_confirm" | "rolling_back"
    InterfaceName string          `json:"interface_name"`
    ConfirmToken  string          `json:"confirm_token"` // 一次性高熵令牌
    TargetURL     string          `json:"target_url"`
    TimeoutSec    int             `json:"timeout_sec"`
    ExpiresAt     time.Time       `json:"expires_at"`
    RollbackCfg   InterfaceConfig `json:"rollback_config"` // 用于回滚的原始配置
}
```

---

## 3. 驱动适配器设计 (`NetworkProvider`)

### 3.1 统一接口
```go
type NetworkProvider interface {
    Name() string
    IsSupported() bool
    ListPhysicalInterfaces(ctx context.Context) ([]InterfaceInfo, error)
    ApplyInterfaceConfig(ctx context.Context, iface string, cfg InterfaceConfig) error
    ResetInterfaceToMaintenance(ctx context.Context, iface string) error
}
```

### 3.2 探测与降级策略
1. **自动探测工厂 (`detectProvider()`)**：
   - 检查 `config.toml` 中是否显式配置了 `[network.custom_script]`，若存在则使用 `ScriptProvider`。
   - 探测系统 D-Bus 是否存在 `org.freedesktop.NetworkManager` 或 `which nmcli` 可执行，若满足则使用 `NetworkManagerProvider`。
   - 探测 `/run/systemd/system` 是否存在且目录 `/etc/systemd/network` 可写，若满足则使用 `SystemdNetworkdProvider`。
   - 开发与交叉编译环境：若都不满足（或在 macOS/Windows 开发机），降级为 `MockProvider`，允许前端正常联调与测试，打出 Warning 日志。

---

## 4. 防掉电两阶段回滚看门狗时序设计

### 4.1 核心流程时序图
```txt
Client (Browser)          Go Host (Zhulong)           Disk Storage           Host Network
       │                          │                         │                     │
       ├─ POST /apply ───────────►│                         │                     │
       │                          ├─ 校验配置与网关互斥       │                     │
       │                          ├─ 生成 Token & 事务 ─────►│ (原子写 fsync)      │
       │                          │  (network_transaction) │                     │
       │◄─ HTTP 200 OK ───────────┤                         │                     │
       │  (token, target_url)     ├─ 启动内存 60s 定时器     │                     │
       │                          ├─ 延时 300ms 后 ──────────────────────────────►│ 重构底层 IP
       │                          │                         │                     │ (原连接断开)
  [前端提示跳转]                  │                         │                     │
       │                          │                         │                     │
   (场景 1: 正常确认)              │                         │                     │
       ├─ POST /confirm?token ───►│                         │                     │
       │                          ├─ 停止定时器             │                     │
       │                          ├─ 删除事务文件 ─────────►│                     │
       │◄─ HTTP 200 (已固化) ─────┤                         │                     │
       │                          │                         │                     │
   (场景 2: 现场断电重启)         │                         │                     │
       │   [Power Cut & Boot]     │                         │                     │
       │                          ├─ Fx OnStart 启动自检     │                     │
       │                          ├─ 发现 pending_confirm ──►│ 读出旧配置          │
       │                          ├─ 强制执行回滚 ───────────────────────────────►│ 恢复旧 IP
       │                          ├─ 清理事务文件 ─────────►│                     │
       │                          │                         │                     │
   (场景 3: 倒计时超时未确认)      │                         │                     │
       │                          ├─ 内存定时器触发 60s      │                     │
       │                          ├─ 执行回滚恢复旧 IP ──────────────────────────►│ 恢复旧 IP
       │                          ├─ 清理事务文件 ─────────►│                     │
```

---

## 5. API 契约设计与端点规格

所有端点均遵循项目规范的 `{ code: "OK", message: "...", data: { ... } }` 响应信封。

### 5.1 端点列表

1. `GET /api/v1/system/network/interfaces`
   - **权限**：Session Authenticated (受保护路由)
   - **响应**：物理网卡列表，包含 `is_current` 标记与当前活跃默认网关。

2. `POST /api/v1/system/network/interfaces/:name/apply`
   - **权限**：Session Authenticated
   - **请求体**：`InterfaceConfig`
   - **校验**：
     - 若 `SetDefault == true`，检查是否有其他网卡也是默认网关，若有则进行互斥处理。
     - 若修改当前进站网卡（`is_current == true`），启动两阶段看门狗事务。
   - **响应**：`{ "transaction_id": "...", "timeout_sec": 60, "target_url": "http://10.0.0.50:8080/#settings", "confirm_token": "..." }`

3. `POST /api/v1/system/network/confirm`
   - **权限**：公开路由（可通过 Query `?token=...` 快捷免密访问）或 Session Authenticated。
   - **逻辑**：校验 Token 与状态机，校验通过则固化新配置并删除落盘事务文件。

4. `POST /api/v1/system/network/rollback`
   - **权限**：Session Authenticated 或携带 `token`。
   - **逻辑**：手动立即中止试运行，瞬间还原上一版本网络配置。

5. `POST /api/v1/system/network/ping`
   - **权限**：Session Authenticated
   - **请求体**：`{ "target": "192.168.1.1" }`
   - **响应**：`{ "reachable": true, "rtt_ms": 1.2 }`

---

## 6. CLI 命令行设计 (`--reset-network`)

在 `cmd/Zhulong/main.go` 中解析 CLI 标志：
```bash
./Zhulong --reset-network
```
- **执行逻辑**：
  1. 不启动 HTTP 服务器与数据库迁移，仅初始化底层网络驱动。
  2. 探测机器上的主物理以太网卡（优先 `eth0` 或第一个枚举到的以太网接口）。
  3. 将该网卡配置为出厂应急静态 IP：
     - IP: `192.168.1.168`
     - 掩码: `255.255.255.0`
     - 网关: 空
  4. 将其余物理网卡重置为 DHCP 客户端模式。
  5. 打印清晰的串口终端提示信息，以退出码 0 干净退出。

---

## 7. 前端 UI/UX 设计与组件拆分

- **路由与导航**：
  - 更新 `web/src/shared/components/layout/Sidebar.tsx`：新增 `settings` 导航项（使用 `Sliders` 图标）。
  - 更新 `web/src/App.tsx`：支持 `#settings` 路由渲染 `SystemSettingsPage`。
- **组件结构 (`web/src/features/systemSettings`)**：
  - `SystemSettingsPage.tsx`：系统设置顶层容器，包含二级 Tab（首期为网络设置）。
  - `components/NetworkSettingsTab.tsx`：网络管理主面板。
  - `components/NetworkCard.tsx`：网卡状态卡片（MAC、状态徽标、当前网口高亮）。
  - `components/NetworkEditModal.tsx`：编辑表单弹窗，支持静态/DHCP 切换与 Ping 诊断。
  - `components/NetworkMigrationModal.tsx`：切 IP 倒计时安全提示模态框。
  - `components/WatchdogCountdownBanner.tsx`：顶部全局悬浮倒计时确认横幅。
- **国际化集成**：
  - 在 `web/src/features/systemSettings/locales/{en,zh-Hans,zh-Hant}.json` 中管理全部字段，并在全局 i18n 资源中注册。

---

## 8. 权能与兼容性矩阵

| 运行环境 | 权限机制 | 状态读取 | 配置下发与持久化 |
| :--- | :--- | :--- | :--- |
| **Ubuntu / Jetson / Armbian** | systemd 赋予 `CAP_NET_ADMIN` 或 root | Linux Netlink + sysfs | NetworkManager (D-Bus / nmcli) |
| **Yocto / 嵌入式 Linux** | systemd 赋予 `CAP_NET_ADMIN` 或 root | Linux Netlink + sysfs | `systemd-networkd` (`/etc/systemd/network`) |
| **极简魔改 BSP** | root 用户 | Linux Netlink + sysfs | 外部 Hook 脚本 (`[network.custom_script]`) |
| **macOS / 开发机 (Dev)** | 普通开发权限 | Go 标准库 `net.Interfaces()` | Mock 驱动，支持完整 UI 调试与测试 |
