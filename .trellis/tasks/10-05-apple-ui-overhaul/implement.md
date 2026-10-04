# Apple 风格主题布局与认证向导 UI 重构 执行计划 (implement.md)

## Ordered Implementation Plan

- [x] **Step 1: 全局主题与基础布局 Token 优化**
  - 文件：`web/src/styles.css`, `web/src/App.tsx`
  - 任务：
    - 精确调整 CSS 变量与 Apple 色板映射（背景灰 `#f5f5f7`、卡片阴影、Spring 减速缓动）。
    - 升级 `.topbar` 磨砂毛玻璃与 44px 控件热区规范。
    - 提升 `.workspace` 留白感与版心视觉。
  - 验证：`pnpm --prefix web test App.test.tsx`

- [x] **Step 2: 重构认证卡片容器 (AuthCard)**
  - 文件：`web/src/features/auth/components/AuthCard.tsx`
  - 任务：
    - 移除冗余边框与杂色，应用 Apple 卡片 `rounded-2xl`、微阴影、大内边距 `p-8 md:p-10`。
    - 优化微徽标与标题层级，加入 Spring 减速动画与优雅错误警示条。
  - 验证：`pnpm --prefix web test AuthGuard.test.tsx`

- [x] **Step 3: 重构登录表单 (LoginForm)**
  - 文件：`web/src/features/auth/components/LoginForm.tsx`
  - 任务：
    - 输入框升级为 Apple 沉浸灰底 `bg-[#f5f5f7] dark:bg-[#3a3a3c]` + `rounded-xl` + 2px 蓝光圈。
    - 登录按钮升级为全圆角胶囊 `rounded-full` + Apple 蓝 `#0071e3` + `active:scale-[0.98]`。
    - 优化加载态 Spinner 与按钮阻尼。
  - 验证：`pnpm --prefix web test LoginForm.test.tsx`

- [x] **Step 4: 重构首次启动初始化向导 (InitForm)**
  - 文件：`web/src/features/auth/components/InitForm.tsx`
  - 任务：
    - 开机助手向导式视觉设计与文案呼吸感。
    - 实现 Apple 风格三段式胶囊密码强度指示条（弱/中/强 平滑染色）。
    - 表单输入框与确认密码联动优化，全圆角胶囊提交按钮。
  - 验证：`pnpm --prefix web test InitForm.test.tsx`

- [x] **Step 5: 用户导航栏轻量美化与交互打磨 (UserNav)**
  - 文件：`web/src/features/auth/components/UserNav.tsx`
  - 任务：
    - 极简药丸徽章 + 44px 触控标准，与全局 TopBar 深度融合。
  - 验证：`pnpm --prefix web test UserNav.test.tsx`

- [x] **Step 6: 全局质量核查与深浅色/三语矩阵自检**
  - 检查命令：
    - `pnpm --prefix web type-check`
    - `pnpm --prefix web lint`
    - `pnpm --prefix web test`
    - `pnpm --prefix web build`
  - 风格红线审查：无 gradient、无 shadow-2xl、无 border-2/4、留白充裕、动效丝滑。

---

## Rollback Points
- 若样式出现不可逆的视觉错位，可针对 `web/src/` 相关文件执行 `git checkout -- web/src/` 回退。
