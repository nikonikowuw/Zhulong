# React 前端目录架构规范

> 特性切片（Feature Slices）布局、公共导出边界（Public Barrel）及 `@/` 别名规则。

---

## 1. 规划目录结构（Proposed Layout）

> ⚠️ 以下为指导后续前端工程建立的规划约定，功能未开始前严禁创建空目录。

```text
web/
  src/
    features/
      audit/                          # 操作与安全审计日志切片
        components/                   # 内部私有子组件 (表格、明细弹窗、筛选栏)
        hooks/                        # TanStack Query 钩子
        api/                          # 强类型 REST 客户端
        types.ts                      # Zod 与 TypeScript 契约
        index.ts                      # 公共导出入口
      camera/                         # 相机管理切片
        components/                   # 内部私有子组件 (单一职责)
        hooks/                        # 封装业务逻辑与 TanStack Query
        api/                          # 强类型 REST 调用与 DTO
        types.ts                      # 模块私有类型
        index.ts                      # ⚠️ 对外公共导出入口 (Public Barrel)
      systemSettings/                 # 系统设置切片 (网络配置、两阶段回滚看门狗)
        components/                   # 网卡看板、编辑弹窗、迁移倒计时模态框
        hooks/                        # useNetworkInterfacesQuery, useNetworkStatusQuery 等
        api/                          # 强类型 REST 客户端 (networkApi.ts)
        types.ts                      # Zod 与 TypeScript 契约
        locales/                      # 三语国际化字典
        index.ts                      # 公共导出入口 (Public Barrel)
      playback/                       # 视频回放切片
      detection/                      # 实时 AI 检测事件与规则配置切片
    shared/                           # 跨业务模块共享的基础构件
      components/                     # 通用 UI 原子组件 (基于 shadcn/ui)
      theme/                          # 深浅双色主题状态与 ThemeProvider
      i18n/                           # 三语国际化 (en, zh-Hans, zh-Hant JSON 字典)
      api/                            # 网络请求客户端基类 (client.ts, ApiError)
    App.tsx                           # 根应用组装
    main.tsx                          # 挂载入口
```

---

## 2. 模块导入与别名铁律

1. **统一 `@/` 别名**：在 `tsconfig.json` 与 `vite.config.ts` 中配置 `@/` 指向 `src/`。严禁超过一级的相对路径（如 `../../../shared/...`）。
2. **跨切片必走公共 `index.ts`**：跨特性引用只能通过其门面导入：

   ```typescript
   import { CameraCard, type Camera } from '@/features/camera'; // ✅ 正确
   // ❌ 严禁穿透导入私有路径: '@/features/camera/components/CameraCard'
   ```

3. **禁止内部反向导入自己的 Barrel**：特性内部文件互相引用时，严禁 `import ... from './index'` 或 `from '@/features/camera'`，防止打包循环依赖。
