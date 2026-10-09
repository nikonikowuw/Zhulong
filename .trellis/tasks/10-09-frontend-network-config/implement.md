# 系统设置网络配置UI与两阶段安全回滚交互 · 执行计划 (Implementation Plan)

## 1. 实施检查清单 (Execution Checklist)

### 阶段 1：API 客户端与 Zod 数据校验层
- [ ] **1.1 封装 Network API** (`web/src/features/settings/network/api/network-api.ts`)
  - 定义 `InterfaceInfo`, `InterfaceConfigPayload`, `ApplyResponse`, `TransactionState`, `PingPayload`, `PingResult`
  - 实现 `getInterfaces`, `applyConfig`, `confirm`, `rollback`, `getStatus`, `ping`
- [ ] **1.2 编写 Zod 表单校验与工具方法** (`web/src/features/settings/network/data/schema.ts`)
  - IPv4 正则校验与合法子网掩码检验算法
  - 动态联合校验（DHCP 模式允许静态项为空，Static 模式必填 IP 与掩码）
- [ ] **1.3 编写 API 与 Schema 单元测试** (`web/src/features/settings/network/data/schema.test.ts`, `api.test.ts`)
  - 验证合法与非法 IP、掩码校验结果

### 阶段 2：数据 Hook 与状态管理层
- [ ] **2.1 封装 TanStack Query Hook** (`web/src/features/settings/network/hooks/use-network.ts`)
  - `useNetworkInterfaces`: 获取网卡列表
  - `useNetworkStatus`: 轮询或获取当前事务状态
  - `useApplyConfigMutation`: 下发配置并触发两阶段事务
  - `useConfirmMutation` & `useRollbackMutation`: 确认与回滚操作
  - `usePingMutation`: 连通性测试

### 阶段 3：UI 组件开发
- [ ] **3.1 网卡卡片与看板列表** (`web/src/features/settings/network/components/interface-card.tsx` & `interface-list.tsx`)
  - 展示接口名、MAC、载波状态（LinkUp Badge）、IPv4 列表、网关、DNS
  - 默认网关（Default Gateway）标识与当前进站网卡（Current / Management）警示标签
  - 骨架屏（Skeleton）与加载失败重试界面
- [ ] **3.2 网卡配置表单对话框** (`web/src/features/settings/network/components/interface-edit-dialog.tsx`)
  - `react-hook-form` + `zodResolver`
  - DHCP / Static 单选切换与动态显隐输入框
  - 默认网关勾选框与进站网卡修改警示
- [ ] **3.3 连通性探测工具弹窗** (`web/src/features/settings/network/components/ping-dialog.tsx`)
  - 输入目标 IP/主机名，展示连通状态与 RTT 延迟指示器
- [ ] **3.4 两阶段安全回滚与迁移模态框** (`web/src/features/settings/network/components/watchdog-modal.tsx`)
  - 60 秒倒计时动态进度条与数字跳动
  - 原地确认 vs 跨 IP 迁移检测与新地址跳转引导
  - 【确认生效】与【立即放弃并回滚】按钮及状态反馈

### 阶段 4：页面组装、路由配置与导航集成
- [ ] **4.1 组装主界面** (`web/src/features/settings/network/index.tsx`)
  - 使用 `ContentSection` 统一布局，集成看板、工具栏（刷新、Ping 工具）与模态框
  - 页面初次加载时检查 `getStatus()` 与 URL `confirm_token` 参数，自动拉起活跃事务
- [ ] **4.2 路由挂载** (`web/src/routes/_authenticated/settings/network.tsx`)
  - 注册 `/settings/network` 路由
- [ ] **4.3 侧边栏与设置导航配置**
  - 在 `web/src/features/settings/index.tsx` 中向 `sidebarNavItems` 加入 Network 选项
  - 在 `web/src/components/layout/data/sidebar-data.ts` 的 Other -> Settings 子项中加入 Network 选项

### 阶段 5：质量保障与验证
- [ ] **5.1 编写组件与交互单元测试**
  - 测试网卡卡片渲染、配置表单交互与两阶段看门狗确认倒计时逻辑
- [ ] **5.2 规范与类型检查**
  - 运行 `pnpm test`
  - 运行 `pnpm lint`
  - 运行 `pnpm build` (验证 TypeScript 与 Vite 构建产物无死链/类型错误)

---

## 2. 验证命令 (Validation Commands)

```bash
# 1. 运行网络模块相关测试与全部单元测试
cd web && pnpm test

# 2. 静态代码分析与规范检查
cd web && pnpm lint

# 3. 生产类型检查与 Vite 打包构建验证
cd web && pnpm build
```

---

## 3. 回滚方案 (Rollback Points)
若集成过程中出现异常或路由冲突，可回滚以下新增与修改文件：
- 撤销 `web/src/features/settings/network/`
- 撤销 `web/src/routes/_authenticated/settings/network.tsx`
- 还原 `web/src/features/settings/index.tsx` 和 `web/src/components/layout/data/sidebar-data.ts`
- 执行 `git checkout -- web/` 即可完全无损还原。
