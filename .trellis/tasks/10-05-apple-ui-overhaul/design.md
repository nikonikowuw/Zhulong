# Apple 风格主题布局与认证向导 UI 重构 技术设计 (design.md)

## 1. 架构与设计原则

本重构坚持「**简单优先、精确修改、高还原度 Apple Style**」的工程原则，不引入额外庞大第三方 UI 库，纯粹基于现有的 **Tailwind CSS v4 + 语义 CSS 变量 + 原生 CSS 特性** 实现高品质 Apple 设计系统。

### 1.1 设计 Token 映射

| 概念 | 浅色 Token | 深色 Token | 对应 Tailwind / CSS 类 |
| :--- | :--- | :--- | :--- |
| **画布背景** | `#f5f5f7` | `#1c1c1e` | `bg-[var(--background)]` / `bg-[#f5f5f7]` |
| **卡片表面** | `#ffffff` | `#2c2c2e` | `bg-[var(--surface)]` / `bg-white dark:bg-[#2c2c2e]` |
| **次级/输入表面** | `#f5f5f7` | `#3a3a3c` | `bg-[var(--surface-muted)]` |
| **悬停表面** | `#ebebf0` | `#48484a` | `hover:bg-[var(--surface-hover)]` |
| **主前景色** | `#1d1d1f` | `#f5f5f7` | `text-[var(--foreground)]` |
| **次前景色 (Muted)**| `#86868b` | `#a1a1a6` | `text-[var(--muted)]` |
| **品牌主色 (Accent)**| `#0071e3` | `#0a84ff` | `bg-[#0071e3]` / `text-[#0071e3]` |
| **按钮悬停** | `#0077ed` | `#2590ff` | `hover:bg-[#0077ed]` |
| **卡片阴影** | `0 4px 12px rgba(0,0,0,0.08)` | `0 4px 12px rgba(0,0,0,0.22)` | `shadow-[0_4px_12px_rgba(0,0,0,0.08)]` |
| **弹簧动效曲线** | `cubic-bezier(0.25, 0.1, 0.25, 1)` | 同左 | `transition-all duration-300 ease-[cubic-bezier(0.25,0.1,0.25,1)]` |
| **按压物理阻尼** | `scale(0.98)` | 同左 | `active:scale-[0.98]` |

---

## 2. 核心组件重构规范

### 2.1 全局主题与布局 (`web/src/styles.css` & `web/src/App.tsx`)
- 维护 `:root` 与 `:root[data-theme="dark"]` 中的 Apple 色板定义。
- 升级 `.topbar`：
  - 增加微磨砂 `backdrop-blur-md` 与高透底色配合。
  - 精简内边距与边框，使品牌徽标（Brand Mark）和右侧用户/国际化/主题控件保持纯净统一的 44px 热区。
- 升级 `.workspace`：
  - 提升留白感（`py-16 md:py-24`），配合最大宽度 `980px` 带来开阔舒适感。

### 2.2 认证卡片与容器 (`AuthCard.tsx`)
- 严格遵循 Apple Card Token：
  - `bg-white dark:bg-[#2c2c2e]`，圆角 `rounded-2xl`，阴影 `shadow-[0_4px_12px_rgba(0,0,0,0.08)]`。
  - 内边距统一扩至 `p-8 md:p-10`。
  - 容器外侧留白 `min-h-[70vh] flex items-center justify-center`。
  - 顶部微徽标以浅灰胶囊托底，居中无杂色。

### 2.3 登录界面 (`LoginForm.tsx`)
- 输入框：
  - Apple 灰内凹表面 `bg-[#f5f5f7] dark:bg-[#3a3a3c]`，无边框或 1px 极细底色。
  - 聚焦蓝光圈 `focus:ring-2 focus:ring-[#0071e3]`。
  - 左右图标与文字内衬精准 12px/16px 留白。
- 按钮：
  - 全圆角胶囊 `rounded-full`，经典 Apple 蓝底色 `#0071e3`。
  - 按压微阻尼 `active:scale-[0.98]`，禁用态透明度平滑过渡。
- 错误提示：
  - 保持 `role="alert"`，Apple 警示红与弱背景，带轻微弹簧微动效。

### 2.4 系统首次启动向导 (`InitForm.tsx`)
- 开机向导视觉：
  - 强化主副标题信息层级，营造首次启动配置的信任感与引导性。
- 三段式微胶囊密码强度指示器：
  - 3 个等宽微圆角胶囊横条并排，间距 4px，高度 4px。
  - 随输入动态填充：
    - Weak: 1 条红 (`#ff3b30`)
    - Medium: 2 条蓝 (`#0071e3`)
    - Strong: 3 条绿 (`#34c759`)
  - 右侧文字紧凑清晰，保留原测试所需的文字内容（Weak / Medium / Strong）。
- 提交按钮同样采用 Apple 胶囊主按钮。

### 2.5 用户导航与操作组件 (`UserNav.tsx`)
- 保持已登录用户的极简药丸徽章（`rounded-full` 浅底色）与退出按钮的轻微悬停阻尼。

---

## 3. 兼容性与回归保证

- **DOM 契约**：所有测试查找的 role（`heading`、`button`、`alert`）与 label 保持绝对稳定。
- **国际化契约**：全部文案直接读取 `t(...)`，中简、中繁、英文三语完整保持。
- **构建测试**：无任何未使用的变量或样式，测试 100% 通过。
