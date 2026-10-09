# 系统单用户登录与初始化技术设计 (Design)

## 1. 架构总览与数据流

系统采用**后端 HttpOnly Cookie + 纯内存轻量会话**模型。前端通过 Axios 发起请求，自动携带凭据并由后端校验。

```txt
┌────────────────────────────────────────────────────────────────────────┐
│                        前端 (React 19 + Vite 8)                        │
│                                                                        │
│   src/routes/(auth)/sign-in.tsx                                        │
│          │                                                             │
│          ▼                                                             │
│   features/auth/sign-in/index.tsx ──[useQuery]──► GET /api/v1/auth/status
│          │                                                             │
│          ├── status.initialized === false ──► <InitForm />             │
│          └── status.initialized === true  ──► <LoginForm />            │
│                                                                        │
│   features/auth/api/auth-api.ts                                        │
│          │                                                             │
│          ▼ (withCredentials: true)                                     │
│   Axios ───────────────────────────────────────────────────────────┐   │
└────────────────────────────────────────────────────────────────────┼───┘
                                                                     │
                                                                     ▼
┌────────────────────────────────────────────────────────────────────────┐
│                       后端 (Go + Gin @ internal/auth)                  │
│                                                                        │
│   /api/v1/auth/status ──► 查询 SQLite 中是否存在记录                   │
│   /api/v1/auth/init   ──► 写入管理员 + bcrypt 哈希 + 下发 Cookie       │
│   /api/v1/auth/login  ──► bcrypt 校验 + 内存 Session 校验 + 下发 Cookie│
│   /api/v1/auth/me     ──► 验证 zhulong_session Cookie 并返回用户信息   │
│   /api/v1/auth/logout ──► 销毁内存 Session + 清除 Cookie               │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 模块拓扑与接口契约

### 2.1 API 客户端与 DTO 契约 (`features/auth/api/auth-api.ts`)

```typescript
export interface AuthUser {
  id: number
  username: string
  createdAt: string
}

export interface AuthStatus {
  initialized: boolean
}

export interface InitAdminPayload {
  username: string
  password: string
  confirmPassword: string
}

export interface LoginPayload {
  username: string
  password: string
}
```

- **Axios 客户端配置**：配置 `baseURL: '/api/v1'`，开启 `withCredentials: true` 以确保跨请求自动携带 `zhulong_session` Cookie。
- **错误拦截统一适配**：响应拦截器解析 `{ code, message, data }` 信封；HTTP 错误状态码时，优先提取 `response.data.message` 供 UI 直接展示。

### 2.2 Zod Schema 契约 (`features/auth/data/schema.ts`)

```typescript
export const loginSchema = z.object({
  username: z.string().min(1, '请输入用户名'),
  password: z.string().min(1, '请输入密码'),
})

export const initAdminSchema = z
  .object({
    username: z
      .string()
      .min(3, '用户名至少 3 个字符')
      .max(32, '用户名最多 32 个字符'),
    password: z
      .string()
      .min(8, '密码长度至少 8 位')
      .max(64, '密码长度最多 64 位'),
    confirmPassword: z.string().min(1, '请确认密码'),
  })
  .refine((data) => data.password === data.confirmPassword, {
    message: '两次输入的密码不一致',
    path: ['confirmPassword'],
  })
```

---

## 3. 状态管理与路由守卫 (State & Route Guard)

### 3.1 `src/stores/auth-store.ts`

- 保存 `user: AuthUser | null`、`isAuthenticated: boolean`、`isChecking: boolean`；
- 提供 `setUser(user)`、`reset()`；
- 提供 `checkAuth()`：调用 `getMe()`，成功则设置用户，失败则清空并设为未登录。

### 3.2 路由守卫机制 (`src/routes/_authenticated/route.tsx`)

- 在 TanStack Router 的 `beforeLoad` 守卫中执行会话判定；
- 若未处于登录态，异步触发 `checkAuth()`；若仍未通过，重定向至 `redirect({ to: '/sign-in', search: { redirect: location.href } })`；
- 避免未授权闪烁。

---

## 4. UI 组件设计与清理

1. **`src/features/auth/sign-in/index.tsx`**：
   - 带有 Zhulong 系统 Logo 和品牌主标题；
   - 响应式居中卡片，利用 `@tanstack/react-query` 请求 `authApi.getStatus()`；
   - 渲染骨架屏、`<InitForm />` 或 `<LoginForm />`。
2. **清理冗余模板代码**：
   - 移除模板中 Facebook/Github 社交登录按钮；
   - 移除无关的 Clerk 路由和引用，避免死代码残留。
