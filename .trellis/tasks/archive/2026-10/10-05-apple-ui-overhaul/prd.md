# Apple 风格主题布局与认证向导 UI 重构 PRD

## Goal

将 Zhulong 系统的前端全局主题布局、单用户登录卡片及系统首次启动初始化向导，全面重构升级为符合 **Apple Style 设计规范** 与 **UI/UX Pro Max 规范** 的极致简约、高端科技质感界面。

---

## 核心风格规则（Apple Style Design System）

1. **绝对禁止**：
   - 严禁渐变背景与文字（`bg-gradient-*`, `background-clip: text`）。
   - 严禁重阴影与内阴影（`shadow-2xl`, `shadow-inner`）。
   - 严禁粗边框（`border-2`, `border-4`）。
   - 严禁元素拥挤、花哨装饰与玻璃态拟态滥用。
2. **必须遵守**：
   - 呼吸感留白与居中优雅对齐（`max-w-[980px]`, `p-8 md:p-12`）。
   - 配色标准：
     - 浅色：背景 `#f5f5f7`，卡片表面 `#ffffff`，前景色 `#1d1d1f`，辅色 `#86868b`。
     - 深色：背景 `#1c1c1e`，卡片表面 `#2c2c2e`，次级表面 `#3a3a3c`，前景色 `#f5f5f7`。
     - 强调色：Apple 蓝 `#0071e3`（深色 `#0a84ff`），成功绿 `#34c759`，错误红 `#ff3b30`。
   - 几何圆角：卡片 `rounded-2xl`，输入框 `rounded-xl`，操作按钮全圆角胶囊 `rounded-full`。
   - 微妙阴影：`shadow-[0_4px_12px_rgba(0,0,0,0.08)]`（深色 `rgba(0,0,0,0.24)`）。
   - 物理动效：缓动曲线统一采用 Apple 弹簧衰减 `cubic-bezier(0.25, 0.1, 0.25, 1)`，交互按钮/卡片附带 `active:scale-[0.98]` 触感阻尼。

---

## Requirements

### 1. 全局主题与基础布局重构 (System Theme & Layout)
- **TopBar 导航栏**：
  - 纯净平滑顶栏，支持半透明微磨砂 `backdrop-blur-md` 浮于内容之上或与背景优雅融合。
  - 品牌 Logo 与设备环境徽章对齐，触控控件（语言切换器、深浅色模式切换按钮、用户态组件）统一遵循 44px 黄金尺寸。
  - 移除多余的重线边框，使用极细微弱分割线（`border-b border-[#d2d2d7]/50` 或深色 `border-[#48484a]/50`）。
- **Workspace 工作区**：
  - 维持 `max-w-[980px]` 经典视口宽度，留白呼吸空间提升至 `py-12 md:py-20`。
  - Footer 版权与硬件信息居中弱化展示。

### 2. 登录界面 UI 重构 (LoginForm)
- **AuthCard 容器**：
  - 升级为 Apple 标准卡片容器：`bg-white dark:bg-[#2c2c2e] rounded-2xl shadow-[0_4px_12px_rgba(0,0,0,0.08)] p-8 md:p-10`。
  - 顶部图标以圆形弱灰底微徽章承载，主标题采用 `-apple-system` 紧凑字距 `font-semibold tracking-tight text-2xl`。
- **输入字段 (Input)**：
  - 浅灰内嵌背景 `bg-[#f5f5f7] dark:bg-[#3a3a3c] rounded-xl text-black dark:text-white placeholder:text-gray-400`。
  - 移除厚重描边，聚焦时呈现丝滑的 Apple 蓝光圈 `focus:ring-2 focus:ring-[#0071e3]`。
- **主操作按钮 (Submit Button)**：
  - Apple 经典胶囊主按钮：`w-full py-3 px-6 rounded-full bg-[#0071e3] hover:bg-[#0077ed] text-white font-medium active:scale-[0.98] transition-all duration-200`。
  - 提交中呈现精致转动 Spinner，保持按压阻尼感。
- **错误提示 (Error Alert)**：
  - 错误提示条采用极简淡粉底 + Apple 红 `#ff3b30`，带微弹簧淡入动画与 `role="alert"`。

### 3. 系统首次启动初始化向导 UI 重构 (InitForm)
- **开机助手 (Setup Assistant) 视觉仪式感**：
  - 突出「欢迎使用 Zhulong · 初始化系统管理员」，清晰的向导式文案层次。
- **密码强度三段胶囊指示器**：
  - 摒弃单行文字颜色变动，设计为三段式微型进度条（弱/中/强）：
    - 弱：第 1 段填充 Apple 红 `#ff3b30`。
    - 中：前 2 段填充 Apple 蓝 `#0071e3`。
    - 强：全部 3 段填充 Apple 绿 `#34c759`。
  - 配套文案使用弱化辅助字号。
- **输入联动与确认匹配反馈**：
  - 密码与确认密码校验反馈清晰，禁用态与可提交态平滑过渡。
- **提交主按钮**：
  - 胶囊按钮 `rounded-full`，带完成图标与顺畅 Loading 态。

### 4. 国际化与可访问性 (i18n & Accessibility)
- 严禁硬编码文本，100% 覆盖中简、中繁、英文三语。
- 保证 WCAG 2.2 AA 文本对比度（≥ 4.5:1）。
- 所有交互控件具有明确的可访问性焦点（`focus-visible`）与屏幕阅读器无障碍标签。

---

## Acceptance Criteria

- [x] **视觉风格核查**：完全杜绝 `bg-gradient-*`、`shadow-2xl`、`border-2/4` 等违规样式，视觉呈现纯正 Apple 灰白蓝经典科技极简风。
- [x] **触感阻尼与动画**：所有按钮与卡片交互具备 `active:scale-[0.98]` 与减速贝塞尔曲线 `cubic-bezier(0.25, 0.1, 0.25, 1)`。
- [x] **登录页面体验**：LoginForm 表单结构、输入框样式、错误展示与胶囊按钮完全符合 Apple 规范，交互流畅。
- [x] **初始化向导体验**：InitForm 具备三段式微胶囊密码强度指示器、开机引导式视觉设计及清晰的校验反馈。
- [x] **深浅色模式适配**：在 Light 与 Dark 两种主题下，所有背景色、文字对比度、阴影均自适应且达到 WCAG AA 标准。
- [x] **三语自检**：在 `en`、`zh-Hans`、`zh-Hant` 下无文本溢出或断裂，文案准确。
- [x] **测试与构建通过**：所有自动化单元测试（Vitest 30+ 项测试）全绿，`tsc --noEmit` 和 `eslint .` 零报错通过。
