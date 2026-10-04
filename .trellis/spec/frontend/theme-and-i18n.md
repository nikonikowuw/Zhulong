# 主题定制与三语国际化规范

> 深浅双色主题、英/简中/繁中全文国际化契约及用户偏好持久化策略。

---

## 1. 深浅双色主题规范 (Light / Dark)

- **语义设计 Token**：严禁硬编码十六进制颜色。统一使用 Tailwind CSS / shadcn/ui 语义 CSS 变量（`--background`, `--foreground`, `--border`, `--muted` 等）。
- **全要素覆盖**：主题必须覆盖所有弹出层（Dialog, Popover, Select）、图表线条与输入框 Focus 环。

---

## 2. 完整三语国际化规范 (English, 简体中文, 繁体中文)

必须完整支持三大官方语言：**`en`**、**`zh-Hans`**、**`zh-Hant`**（繁体中文为正式必选）。

1. **100% 文本国际化**：标题、按钮、占位符、Zod 校验报错、Toast 提示、图例及 ARIA 标签全部包裹 `t(...)`，严禁 JSX 中出现硬编码文本。
2. **用户自输入数据保留原样**：相机自定义名称、IP、密码等用户输入原文展示，仅翻译包裹它们的系统 Label。
3. **后端错误直接 Toast 弹出 (后端翻译)**：
   - 前端所有 API 请求在 Header 中自动携带用户当前语言（`Accept-Language: en` / `zh-Hans` / `zh-Hant`）。
   - 后端直接返回翻译后的本地化 `message`；前端捕获异常后直接调用 **Toast 组件（如 shadcn/ui Toast / Sonner）** 输出展示：`toast.error(error.message)`。前端 i18n 字典专注 UI 静态文本，无需在前端重复维护后端错误码字典。

---

## 3. 用户偏好持久化与首访行为规则

```txt
首次访问:
  主题: 自动匹配系统 (prefers-color-scheme)
  语言: 自动匹配浏览器 (navigator.languages) ➔ 均不匹配则默认 fallback 为英文 (en)
用户手动切换:
  偏好显式持久化至 localStorage (拥有最高优先级，不再被系统偏好覆盖)
```

---

## 4. 质量验证：六种组合全矩阵自检

必须在**六种组合（2 种主题 × 3 种语言）**下完整自检：

1. `Light + en`：排版折行与文本截断检查。
2. `Light + zh-Hans`：标准浅色中文。
3. `Light + zh-Hant`：繁体字形与词汇习惯。
4. `Dark + en`：深色对比度（WCAG 4.5:1）。
5. `Dark + zh-Hans`：深色中文。
6. `Dark + zh-Hant`：深色繁体。
