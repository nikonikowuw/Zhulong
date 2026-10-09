# 系统单用户登录与初始化执行计划 (Implement)

---

## 阶段规划与执行检查清单

- [ ] **1. API 基础客户端与 Auth DTO 契约建设**
  - 在 `src/lib/api-client.ts` 封装带凭据和统一信封拦截的 Axios 客户端；
  - 在 `src/features/auth/api/auth-api.ts` 实现 `getStatus`, `initAdmin`, `login`, `logout`, `getMe`；
  - 在 `src/features/auth/data/schema.ts` 定义 `loginSchema` 与 `initAdminSchema`；
  - **验证**：编写 `src/features/auth/data/schema.test.ts`，验证必填校验、密码长度与两次密码一致性规则通过。

- [ ] **2. 重构全局用户状态存储 (`src/stores/auth-store.ts`)**
  - 适配真实用户类型 `{ id, username, createdAt }`；
  - 接入 `checkAuth()` 会话探测方法；
  - **验证**：更新并运行 `src/stores/auth-store.test.ts` 单元测试通过。

- [ ] **3. 实现核心表单组件与交互**
  - 实现 `src/features/auth/components/login-form.tsx`（支持密码显隐、Pending 状态、错误 Toast）；
  - 实现 `src/features/auth/components/init-form.tsx`（首次安装初始化向导、密码匹配校验）；
  - **验证**：编写组件测试，验证表单必填阻断与提交调用。

- [ ] **4. 重构登录主页面与路由接入**
  - 重构 `src/features/auth/sign-in/index.tsx`：注入 TanStack Query 状态探测，根据 `initialized` 动态切换初始化与登录表单；
  - 接入 `src/routes/(auth)/sign-in.tsx` 路由，处理已登录自动回跳与 `redirect` 参数；
  - **验证**：页面骨架屏、状态探测和表单分支正确呈现。

- [ ] **5. 路由守卫与控制台注销退出联动**
  - 在 `src/routes/_authenticated/route.tsx` 的 `beforeLoad` 中增加登录校验守卫；
  - 在 `src/components/layout/nav-user.tsx` 中绑定真实用户名展示并实现安全注销回调；
  - **验证**：未登录直接访问受保护页面被正确拦截并重定向到 `/sign-in`。

- [ ] **6. 模板精简与死代码清理**
  - 移除无用的第三方社交登录（Facebook/GitHub 图标与死代码）；
  - 清理未使用的 Clerk 路由及组件；
  - **验证**：运行 `pnpm knip` 与 `pnpm lint` 确保无无效依赖与代码报错。

- [ ] **7. 最终质量门禁基线验证**
  - 运行 `pnpm lint` 静态检查；
  - 运行 `pnpm build`（`tsc -b && vite build`）验证生产打包；
  - 运行 `pnpm test` 验证单元与组件测试。

---

## 门禁指令与验证

```bash
cd web
pnpm lint            # ESLint 静态代码检查
pnpm build           # tsc -b 强类型编译 + Vite 生产打包
pnpm test            # 单元与组件测试
```
