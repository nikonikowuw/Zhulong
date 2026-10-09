# 系统设置网络配置UI与两阶段安全回滚交互 · 技术架构设计 (Technical Design)

## 1. 架构定位与系统边界

本模块位于前端应用（`web/src/features/settings/network`），属于「系统设置」板块的关键基础设施模块。向下通过 Axios 客户端（`@/lib/api-client`）与后端 `/api/v1/system/network/*` 交互，向上遵从 TanStack Query 服务端状态管理规范与 TanStack Router 强类型文件路由规范。

```txt
┌────────────────────────────────────────────────────────────────────────┐
│                        TanStack Router 路由层                          │
│               src/routes/_authenticated/settings/network.tsx           │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
┌───────────────────────────────────▼────────────────────────────────────┐
│                  网络配置特性模块 (features/settings/network)            │
│  ┌──────────────────────────────────────────────────────────────────┐  │
│  │ SettingsNetwork (主容器)                                          │  │
│  │ ├─ InterfaceList & InterfaceCard (网卡卡片列表 + 状态徽章)         │  │
│  │ ├─ PingDialog (网络连通性探测测试工具)                           │  │
│  │ ├─ InterfaceEditDialog (网卡配置表单 - react-hook-form + zod)    │  │
│  │ └─ WatchdogConfirmModal (两阶段倒计时确认与地址迁移提示模态框)    │  │
│  └───────────────────────────────────┬──────────────────────────────┘  │
│                                      │ Hook 驱动 (逻辑与渲染解耦)      │
│  ┌───────────────────────────────────▼──────────────────────────────┐  │
│  │ 自定义 Hook: useNetworkInterfaces & useNetworkTransaction       │  │
│  │ ├─ 查询缓存 (useQuery): 获取网卡列表与未决事务状态                  │  │
│  │ └─ 变更事务 (useMutation): apply / confirm / rollback / ping      │  │
│  └───────────────────────────────────┬──────────────────────────────┘  │
└──────────────────────────────────────┼─────────────────────────────────┘
                                       │ HTTP RESTful API (Envelope)
┌──────────────────────────────────────▼─────────────────────────────────┐
│               后端接口层 (/api/v1/system/network/*)                     │
│  GET /interfaces | POST /interfaces/:name/apply | GET /status          │
│  POST /confirm   | POST /rollback               | POST /ping           │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 领域模型与 API 契约 (`network-api.ts`)

### 2.1 数据模型定义
```typescript
// 物理网卡运行状态模型 (对齐后端 InterfaceInfo)
export interface InterfaceInfo {
  name: string            // 网卡名称，例如 "eth0"
  mac: string             // 物理 MAC 地址
  linkUp: boolean         // 物理链路载波状态
  mode: 'dhcp' | 'static' // 配置模式
  ipAddresses: string[]   // 当前生效 IPv4 CIDR 列表，如 ["192.168.1.100/24"]
  gateway: string         // 网关
  dns: string[]           // DNS 服务器列表
  isDefaultGw: boolean    // 是否承载全局默认路由
  isCurrent: boolean      // 是否为当前 HTTP 请求进站网卡
}

// 网卡配置修改负载 (对齐后端 InterfaceConfig)
export interface InterfaceConfigPayload {
  mode: 'dhcp' | 'static'
  ipAddress?: string
  subnetMask?: string
  gateway?: string
  dns?: string[]
  setDefault?: boolean
}

// 提交配置申请响应 (对齐后端 ApplyResponse)
export interface ApplyResponse {
  transactionId: string
  timeoutSec: number
  targetUrl: string
  confirmToken: string
}

// 事务状态模型 (对齐后端 TransactionState)
export interface TransactionState {
  transactionId: string
  status: 'idle' | 'pending_confirm' | 'rolling_back'
  interfaceName: string
  confirmToken: string
  targetUrl: string
  timeoutSec: number
  expiresAt: string
  rollbackConfig: InterfaceConfigPayload
}

// 连通性探测请求与响应 (对齐后端 PingRequest / PingResponse)
export interface PingPayload {
  target: string
}

export interface PingResult {
  reachable: boolean
  rttMs: number
}
```

### 2.2 API 方法封装
```typescript
export const networkApi = {
  getInterfaces(): Promise<InterfaceInfo[]>,
  applyConfig(name: string, payload: InterfaceConfigPayload): Promise<ApplyResponse>,
  confirm(token?: string): Promise<{ status: string }>,
  rollback(token?: string): Promise<{ status: string }>,
  getStatus(): Promise<TransactionState>,
  ping(target: string): Promise<PingResult>,
}
```

---

## 3. 组件分层与职责划分

| 组件 / 模块 | 文件路径 | 职责与模式 |
| :--- | :--- | :--- |
| **API 服务** | `features/settings/network/api/network-api.ts` | 强类型 API 请求封装，处理 Axios 信封解包 |
| **Schema 校验** | `features/settings/network/data/schema.ts` | Zod 表单校验（IPv4 正则、掩码校验、模式互斥校验） |
| **主页面容器** | `features/settings/network/index.tsx` | 装配 ContentSection、查询初始化、协调各个 Dialog 状态 |
| **网卡卡片列表** | `features/settings/network/components/interface-list.tsx` | 网卡卡片排版、骨架屏、空状态、错误状态提示 |
| **网卡卡片** | `features/settings/network/components/interface-card.tsx` | 单网卡信息展示、LinkUp 徽章、IP/网关/DNS、当前进站警告 |
| **配置对话框** | `features/settings/network/components/interface-edit-dialog.tsx` | 基于 `react-hook-form` 的 DHCP/Static 配置表单与提交 |
| **连通性探测弹窗** | `features/settings/network/components/ping-dialog.tsx` | 目标 IP/域名连通性测试与 RTT 毫秒展示 |
| **看门狗确认模态框** | `features/settings/network/components/watchdog-modal.tsx` | 两阶段安全倒计时、目标 URL 迁移跳转、确认与回滚按钮 |
| **数据 Hook** | `features/settings/network/hooks/use-network.ts` | TanStack Query 封装：列表缓存刷新、未决事务轮询与变更 |

---

## 4. 关键交互流程与状态机

### 4.1 网卡配置提交与两阶段回滚交互 (Watchdog Transaction)

```txt
用户修改网卡配置并点击保存
         │
         ▼
[InterfaceEditDialog] 表单 Zod 校验
         │ (通过)
         ▼
判断是否为当前进站网卡 (isCurrent === true)
  ├─ 是：弹出高风险警告「当前操作将修改管理网卡，网络将重构」
  └─ 否：正常流程
         │
         ▼
调用 networkApi.applyConfig(name, payload)
         │ (200 OK)
         ▼
启动 [WatchdogConfirmModal]
  ├─ 提取 timeoutSec (例如 60s) 启动秒级倒计时
  ├─ 保存 confirmToken
  ├─ 比对 targetUrl 与当前 window.location：
  │    ├─ 若发生变化 (IP 改变)：
  │    │    提示「设备 IP 已重构，请在新地址确认生效！」
  │    │    提供链接携带 `?confirm_token=${token}` 跳转至 targetUrl
  │    └─ 若未变化 (同网段或非进站网卡)：
  │         提示「网络正在试运行，请在倒计时结束前验证并确认」
  │
  ├─ 用户点击【确认生效】：
  │    调用 networkApi.confirm(token) ──► 成功 ──► 提示已固化 ──► 关闭模态框并刷新列表
  │
  ├─ 用户点击【放弃并回滚】：
  │    调用 networkApi.rollback(token) ──► 成功 ──► 提示已恢复 ──► 关闭模态框并刷新列表
  │
  └─ 倒计时结束 (超时)：
       模态框提示「倒计时结束，系统已自动触发安全回滚」 ──► 刷新列表
```

### 4.2 刷新页面后的未决事务自动恢复
页面挂载时调用 `networkApi.getStatus()`：
- 若返回 `status === 'pending_confirm'`，且 `expiresAt` 大于当前时间：
  - 计算剩余秒数 `Math.max(0, Math.floor((new Date(expiresAt).getTime() - Date.now()) / 1000))`；
  - 自动唤起 `WatchdogConfirmModal`，恢复未完成的确认流程，避免运维人员因刷新页面而导致网络意外回滚。
- 若 URL 带有 `confirm_token` 参数，自动读取并填充至确认流程中。

---

## 5. 校验规则与边界安全设计

1. **IPv4 格式校验**：
   - 静态 IP、子网掩码、网关、DNS 必须通过 `^((25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(25[0-5]|2[0-4]\d|[01]?\d\d?)$` 严格正则。
2. **合法子网掩码校验**：
   - 校验点分十进制转换为二进制后必须满足连续的 1 后跟连续的 0，如 `255.255.255.0` (/24)、`255.255.0.0` (/16) 等。
3. **零 any 与健壮类型安全**：
   - 严格避免显式 `any`，网络响应经过强类型 DTO 约束，并提供默认兜底。
