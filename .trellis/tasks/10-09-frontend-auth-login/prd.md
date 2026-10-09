# 系统单用户登录与初始化页面实现 PRD

## Goal

在 `satnaing/shadcn-admin` 前端工程中，基于 Zhulong 后端已就绪的单用户鉴权 REST API（`/api/v1/auth/*`），完整实现系统初始化探测、管理员账户初始化、常规密码登录、Cookie 会话保持、受保护路由鉴权守卫与安全注销退出链路。

---

## Requirements

### R1: 系统初始化状态探测与分支渲染
- 用户访问 `/sign-in` 页面时，前端首先请求 `GET /api/v1/auth/status`；
- 在请求返回前，展示卡片骨架屏（Skeleton）；
- 若返回 `{ initialized: false }`，呈现「初始化管理员账号」界面；
- 若返回 `{ initialized: true }`，呈现「系统管理员登录」界面；
- 若当前已拥有有效登录态（`GET /api/v1/auth/me` 成功），直接重定向至控制台首页或 `redirect` 目标路径。

### R2: 管理员首次初始化表单 (Setup Flow)
- 字段包含：管理员用户名（默认推荐 `admin`，支持修改，3~32 字符）、密码（至少 8 字符，最高 64 字符）、确认密码；
- 采用 `react-hook-form` + `zod` 在客户端进行强类型即时校验（前后密码一致性、长度约束）；
- 提交调用 `POST /api/v1/auth/init`；
- 提交中置灰按钮并展示加载动画（Loader）；
- 成功后由后端写入凭证并下发 `zhulong_session` HttpOnly Cookie，前端将返回的用户信息写入 `useAuthStore` 并无缝导航至目标页面。

### R3: 管理员日常登录表单 (Login Flow)
- 字段包含：用户名（必填）、密码（必填）；
- 密码输入框集成显隐切换按钮（复用 `PasswordInput`）；
- 提交调用 `POST /api/v1/auth/login`；
- 成功后更新 `useAuthStore` 用户信息，并优先跳转至 URL 查询参数指定的 `redirect` 路径（若无则跳转 `/`）；
- 失败时（如密码错误、触发限流锁定），捕获后端返回的已本地化错误消息并通过 `sonner` Toast 告警。

### R4: 会话恢复与全局路由鉴权守卫
- 改造 `src/stores/auth-store.ts`，存储真实用户模型 `{ id: number, username: string, createdAt: string }`；
- 提供 `checkAuth()` 方法请求 `GET /api/v1/auth/me`，用于应用启动或路由切换时的会话恢复；
- 在 `src/routes/_authenticated/route.tsx` 中增加路由守卫，未登录用户访问受保护路由统一重定向至 `/sign-in` 并附带当前路径 `redirect` 参数；
- 捕获全局 HTTP 401，自动清除前端用户信息并重定向至 `/sign-in`。

### R5: 顶栏注销联动 (Logout Flow)
- 在控制台侧边栏/顶栏的用户下拉菜单（`nav-user.tsx`）中展示真实管理员用户名与角色标识；
- 点击「退出登录」时触发调用 `POST /api/v1/auth/logout`，清空本地 `auth-store` 并跳转回 `/sign-in`。

### R6: 模板代码精简与设计规范
- 移除 `shadcn-admin` 模板中不适用的第三方社交登录（GitHub / Facebook）、Clerk 路由及注册（Sign-up）/找回密码链接；
- 严格遵循 Tailwind CSS v4 OKLCH 语义颜色系统，完美兼容 Light / Dark 双色模式与 RTL 排版。

---

## Acceptance Criteria

- [ ] AC1: 访问 `/sign-in` 时具备加载骨架屏，根据 `/api/v1/auth/status` 结果准确分支到「初始化」或「登录」视图。
- [ ] AC2: 初始化表单具备 Zod 校验，两次密码不一致或长度不足时给出清晰行内校验错误。
- [ ] AC3: 首次初始化管理员提交成功后，直接建立登录态并进入控制台。
- [ ] AC4: 登录表单支持密码显隐切换，提交时具备 Pending 加载状态。
- [ ] AC5: 输入错误密码时，精准展示后端返回的错误 Toast，无未捕获异常。
- [ ] AC6: 登录成功后正确处理 URL `redirect` 重定向逻辑。
- [ ] AC7: 未登录用户直接访问受保护页面（如 `/` 或 `/tasks`）时被守卫拦截并重定向到 `/sign-in`。
- [ ] AC8: 用户菜单点击退出登录能向后端发送 `/api/v1/auth/logout`，并彻底重置前端登录态。
- [ ] AC9: 清理无关的 Clerk 与第三方登录死代码，通过 `pnpm lint`、`pnpm build` 与前端测试套件。
