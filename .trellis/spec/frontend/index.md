# React 前端开发规范总览

> React 18+ Web SPA 架构设计、所有者 11 条编码铁律、主题/国际化契约及质量门禁。

---

## 1. 规范索引

| 规范指南 | 核心内容 |
| --- | --- |
| [目录架构](./directory-structure.md) | 特性切片（Feature Slices）、公共 `index.ts` 导出边界、`@/` 别名 |
| [跨语言命名规范](../naming-guidelines.md) | 文件名、React 组件、Hook、函数、变量和领域术语 |
| [编码铁律与状态指南](./development-guidelines.md) | 所有者 11 条编码铁律、TanStack Query 服务端状态、Zustand 客户端状态 |
| [主题与三语国际化契约](./theme-and-i18n.md) | 深浅双色主题、英/简中/繁中全文 i18n、本地偏好持久化及回退策略 |
| [质量检验与测试规范](./quality-guidelines.md) | Vitest、RTL 测试、禁止 any 静态校验、Vite 嵌入构建验证 |

---

## 2. 开发前检查清单 (Pre-Development Checklist)

- [ ] **单一职责与按需拆分**：组件是否专注于单一职责？独立子视图是否已拆分子组件，避免生硬切片？
- [ ] **命名自然清晰**：组件、Hook、函数与变量名是否见名知意，并符合 TypeScript/React 惯例？
- [ ] **纯粹渲染与抽 Hook**：组件内是否仅做 UI 渲染？数据请求与衍生计算是否抽至自定义 Hook？
- [ ] **严禁 `any`**：Props 与函数签名是否强类型定义，无任何 `any`？
- [ ] **服务端状态独占**：API 数据是否全部由 TanStack Query 管理，严禁同步到全局 Store？
- [ ] **特性切片门面边界**：跨功能引用是否严格从 `@/features/<module>` 的 `index.ts` 导入？
- [ ] **三状态完备性**：异步交互是否全部处理了 Loading、Error 与 Empty 三态？
- [ ] **全文三语国际化**：所有可见文本是否包裹 `t(...)`？严禁硬编码英文/中文文本？

---

## 3. 质量验证基线

```bash
# 在 web/ 根目录下执行
npm run lint         # ESLint (含禁止 any 校验)
npm run type-check   # tsc --noEmit
npm run test         # Vitest 单元与组件交互测试
npm run build        # Vite 生产构建 (输出至 Go 嵌入目录)
```
