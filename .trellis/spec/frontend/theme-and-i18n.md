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
3. **后端错误直接展示 (后端翻译)**：前端 API 请求携带当前 `Accept-Language`；后端返回已本地化的安全 `message`，前端不重复翻译后端错误码。切换 UI 语言时同步更新 `<html lang>`，让辅助技术获得正确的文本语言。
4. **时间本地化显示**：API 时间戳按 RFC3339Nano UTC 接收并解析；用 `Intl.DateTimeFormat(i18n.resolvedLanguage, ...)` 格式化展示，默认使用浏览器本地时区。若产品需要显示设备/站点时区，必须使用明确配置的 IANA 时区；不得依赖宿主本地时区或手工切割时间字符串。`YYYY-MM-DD` 纯日期不做时区转换，格式化后的显示文本不得回传 API。
5. **特性切片共置 (Feature-Colocated i18n)**：严禁在 `shared/i18n/locales` 中维护单体超大 JSON。各业务模块文案独立共置于 `features/<feature>/locales/{en,zh-Hans,zh-Hant}.json`，并通过特性公共入口 `index.ts` 导出；`shared/i18n/locales` 仅保留全站通用的基础布局词条。各语言在 `shared/i18n/index.ts` 集中挂载，保持 `t("<feature>.<key>")` 调用形态。

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
