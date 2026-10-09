# 主题排版与多语言国际化契约

> 基于 Tailwind CSS v4 + OKLCH 语义色彩体系、多维 Context 驱动（Theme / Font / Direction）与三语国际化规范。

---

## 1. Tailwind CSS v4 + OKLCH 语义主题规范

系统采用现代化 **OKLCH 颜色空间**，在 `src/styles/theme.css` 中定义底层 CSS 变量，并通过 Tailwind CSS v4 的 `@theme inline` 注册为设计令牌（Design Tokens）。

### 1.1 语义 Token 矩阵

| 语义变量 | CSS 变量名 | 用途说明 |
| :--- | :--- | :--- |
| **画布背景** | `--background`, `--foreground` | 页面最底层背景与主文字颜色 |
| **卡片/容器** | `--card`, `--card-foreground` | 业务卡片、面板容器背景与内容文字 |
| **弹出浮层** | `--popover`, `--popover-foreground` | 下拉菜单、气泡提示、浮窗容器 |
| **品牌主色** | `--primary`, `--primary-foreground` | 核心行动点按钮、高亮选中标签 |
| **次要色** | `--secondary`, `--secondary-foreground` | 次级按钮、轻量辅助块 |
| **弱化状态** | `--muted`, `--muted-foreground` | 次要说明文字、禁用背景、分隔装饰 |
| **交互高亮** | `--accent`, `--accent-foreground` | 列表项 Hover 悬停背景、活动态指示 |
| **破坏/危险** | `--destructive` | 删除、销毁、紧急停机等危险警示 |
| **边框与控件** | `--border`, `--input`, `--ring` | 控件边框、输入框线框、键盘 Focus 焦点环 |
| **侧边栏专用** | `--sidebar`, `--sidebar-border`, 等 | 侧边栏框架独立色彩 Token |

### 1.2 编写红线与规范

1. **绝对禁止硬编码绝对颜色**：
   - ❌ 严禁出现 `bg-[#ffffff]`, `text-[#000]`, `border-[#e5e7eb]` 等任意 HEX/RGB 写法；
   - ✅ 统一使用语义工具类：`bg-background`, `text-foreground`, `bg-card`, `border-border`, `text-muted-foreground`。
2. **深浅双色切换机制**：
   - 通过 `ThemeProvider` 动态向 `<html>` 注入或移除 `.dark` 类；
   - 由 CSS 规则 `@custom-variant dark (&:is(.dark *));` 自动匹配激活深色变量，组件内无需手动编写冗余的深浅条件分支。

---

## 2. 布局方向与 RTL 适配契约 (Direction & RTL)

系统通过 `DirectionProvider`（位于 `src/context/direction-provider.tsx`）提供全站 **LTR（左至右）** 与 **RTL（右至左）** 双向支持。

### ⚠️ RTL 编写铁律（逻辑属性优先）

为了确保在阿拉伯语或从右至左语言环境下布局自然镜像翻转，**严禁使用物理方向工具类**，必须强制使用 Tailwind 逻辑属性类：

| 物理属性 (❌ 严禁使用) | 逻辑属性 (✅ 必须使用) | 对应 CSS 效果 |
| :--- | :--- | :--- |
| `pl-*`, `pr-*` | `ps-*`, `pe-*` | `padding-inline-start`, `padding-inline-end` |
| `ml-*`, `mr-*` | `ms-*`, `me-*` | `margin-inline-start`, `margin-inline-end` |
| `left-*`, `right-*` | `start-*`, `end-*` | `inset-inline-start`, `inset-inline-end` |
| `border-l-*`, `border-r-*` | `border-s-*`, `border-e-*` | `border-inline-start`, `border-inline-end` |
| `text-left`, `text-right` | `text-start`, `text-end` | 文本沿排版起始/结束方向对齐 |

---

## 3. 完整三语国际化契约 (English, 简体中文, 繁体中文)

系统必须完备支持三大官方语言：**`en`**、**`zh-Hans`**（简体中文）、**`zh-Hant`**（繁体中文）。

1. **100% 文本国际化抽取**：
   - 页面标题、表格表头、表单 Label、Placeholder、Zod 校验错误、Toast 通知、操作菜单及 ARIA 无障碍标签全部包裹 `t(...)`；
   - 严禁在 JSX 中直接裸写中文或英文硬编码字符串。
2. **用户自输入数据保留原样**：
   - 相机自定义名称、IP、流地址、设备型号等用户输入内容原样呈现，仅对系统字段标签与状态做本地化。
3. **后端错误直接展示 (后端驱动 i18n)**：
   - 前端 Axios/Fetch 请求头必须携带当前语言 `Accept-Language: <lang>`；
   - 后端返回统一信封格式 `{ code, message, data }`，前端直接展示后端返回的已本地化安全 `message`，严禁在前端硬编码映射后端业务错误码。
4. **时间与时区本地化展示**：
   - 接口传输统一采用 RFC3339Nano UTC 格式；
   - 前端通过 `Intl.DateTimeFormat(locale, ...)` 格式化为当前语言与浏览器本地时区的文本展示。

---

## 4. 全矩阵质量验证自检

前端交付前，必须在以下组合下进行样式与排版验证：

- [ ] **Light 模式**：对比度合格，边框清晰，无文字断裂；
- [ ] **Dark 模式**：夜间对比度符合 WCAG AA 级标准（4.5:1），弹出层背景层级清晰；
- [ ] **RTL 镜像验证**：切换至 `rtl` 时，侧边栏、表单图标、面包屑、表格列按预期自然翻转；
- [ ] **三语字符长度容忍**：英文长单词在按钮、导航项中不溢出、不破坏布局高度。
