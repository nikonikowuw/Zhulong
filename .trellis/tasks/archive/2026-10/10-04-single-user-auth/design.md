# 单用户登录与初始化模块技术设计 (design.md)

## 1. 架构定位与分层边界

```txt
React Web UI
  ├── AuthContext / useAuth (初始化/认证/注销全局状态)
  ├── AuthGuard (路由与视图门禁：未初始化 -> InitForm, 未登录 -> LoginForm, 已登录 -> 主界面)
  └── shared/api/client.ts (fetch 配置 credentials: "include", 401 全局失效回调)
         │  HTTP /api/v1/auth/* (Cookie: zhulong_session)
         ▼
Go HTTP 边界 (internal/app, internal/auth)
  ├── handler.go (路由组装、参数绑定与 HTTP 响应)
  ├── middleware.go (RequireAuth 中间件、Cookie 提取与会话注入)
  └── requests.go & responses.go (强类型 DTO，彻底隔离内部数据模型)
         │
         ▼
Go 核心领域与服务层 (internal/auth)
  ├── service.go (AuthService：密码哈希比对、防爆破限流、会话生命周期调度)
  ├── session.go (MemorySessionStore：纯内存并发安全 Session 字典与 TTL)
  └── store.go (UserStore：SQLite GORM 单用户数据库读写，对应 user.go 实体)
         │
         ▼
SQLite 数据库 (internal/database/migrations)
  └── 000002_create_users_table.up.sql / down.sql
```

---

## 2. 数据库设计 (SQLite Migration)

新增迁移脚本 `000002_create_users_table.up.sql`：

```sql
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

**设计要点**：
- 严格遵循单一职责与最简设计，仅保留 `users` 表存储单用户凭据。
- 绝不冗余创建 `sessions` 表，彻底保护 Flash 介质寿命。

---

## 3. Go 后端模块设计 (`internal/auth`)

告别大而全的 `model.go` 杂物箱，按项目规范 `naming-guidelines.md` 采用**见名知意、职责高度单一**的文件结构：

```text
internal/auth/
  ├── handler.go         # HTTP Handlers (status, init, login, logout, me, RegisterRoutes)
  ├── service.go         # 核心业务逻辑 (AuthService: bcrypt 密码校验、限流器集成)
  ├── store.go           # 数据持久化层 (UserStore 接口与 GORM 实现)
  ├── session.go         # 纯内存会话存储 (MemorySessionStore + RWMutex + 自动 TTL)
  ├── middleware.go      # Gin 鉴权中间件 (RequireAuth 与 CurrentUser 上下文存取器)
  ├── requests.go        # HTTP 入参 DTO (InitAdminRequest, LoginRequest 带 binding 校验)
  ├── responses.go       # HTTP 出参 DTO (AuthStatusResponse, UserResponse)
  └── user.go            # GORM 数据库实体模型 (User 结构体)
```

### 3.1 实体模型 (`user.go`)
```go
package auth

import "time"

// User 是单用户管理员在 SQLite 中的持久化实体
type User struct {
    ID           int64     `gorm:"primaryKey;autoIncrement"`
    Username     string    `gorm:"column:username;unique;not null"`
    PasswordHash string    `gorm:"column:password_hash;not null"`
    CreatedAt    time.Time `gorm:"column:created_at"`
    UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (User) TableName() string {
    return "users"
}
```

### 3.2 请求与响应 DTO (`requests.go` & `responses.go`)

**入参 (`requests.go`)**：
```go
package auth

type InitAdminRequest struct {
    Username        string `json:"username" binding:"required,min=3,max=32"`
    Password        string `json:"password" binding:"required,min=8,max=64"`
    ConfirmPassword string `json:"confirmPassword" binding:"required"`
}

type LoginRequest struct {
    Username string `json:"username" binding:"required"`
    Password string `json:"password" binding:"required"`
}
```

**出参 (`responses.go`)**：
```go
package auth

import "time"

type AuthStatusResponse struct {
    Initialized bool `json:"initialized"`
}

type UserResponse struct {
    ID        int64     `json:"id"`
    Username  string    `json:"username"`
    CreatedAt time.Time `json:"createdAt"`
}
```

### 3.3 纯内存会话管理器 (`session.go`)
```go
type SessionItem struct {
    UserID    int64
    Username  string
    ExpiresAt time.Time
}

type MemorySessionStore struct {
    mu       sync.RWMutex
    ttl      time.Duration
    sessions map[string]SessionItem
}

func NewMemorySessionStore(ttl time.Duration) *MemorySessionStore
func (s *MemorySessionStore) Create(userID int64, username string) (string, error)
func (s *MemorySessionStore) Get(token string) (SessionItem, bool)
func (s *MemorySessionStore) Delete(token string)
```

### 3.4 业务服务与防爆破限流 (`service.go`)
- `AuthService` 定义清晰的业务契约：
  - `GetStatus(ctx context.Context) (bool, error)`
  - `InitAdmin(ctx context.Context, req InitAdminRequest) (UserResponse, string, error)`
  - `Login(ctx context.Context, clientIP string, req LoginRequest) (UserResponse, string, error)`
  - `Logout(token string)`
  - `GetCurrentUser(ctx context.Context, userID int64) (UserResponse, error)`
- 密码校验：`bcrypt.CompareHashAndPassword`。
- 限流策略：内存滑动窗口，单 IP 失败 5 次封禁 15 分钟。

### 3.5 HTTP Handler 与中间件 (`handler.go` & `middleware.go`)
- `handler.go`：解析 request DTO，调用 `AuthService`，设置 Cookie `zhulong_session`，使用 `httputil.Success` 返回 response DTO。
- `middleware.go`：实现 `RequireAuth` 中间件，读取 Cookie 并通过 Setter/Getter 安全函数注入 `CurrentUser`。

---

## 4. React 前端切片设计 (`web/src/features/auth`)

保持与后端一一对应的清晰结构：
```text
web/src/features/auth/
  ├── api/
  │   └── authApi.ts          # 强类型 API 接口调用 (status, init, login, logout, me)
  ├── components/
  │   ├── AuthGuard.tsx       # 全局入口守卫
  │   ├── InitForm.tsx        # 首次初始化向导卡片
  │   └── LoginForm.tsx       # 登录表单卡片
  ├── context/
  │   └── AuthContext.tsx     # 状态管理与提供器
  ├── hooks/
  │   └── useAuth.ts          # 便捷 Hook
  ├── types.ts                # TypeScript DTO 与状态枚举
  └── index.ts                # 公开导出 (Public Barrel)
```

---

## 5. 国际化与错误字典契约

在 `internal/httputil/locale.go` 及前端三语字典中完整覆盖：
- `UNAUTHORIZED`: 需先登录
- `INVALID_CREDENTIALS`: 用户名或密码错误
- `SYSTEM_ALREADY_INITIALIZED`: 系统已初始化，禁止重复设置
- `SYSTEM_NOT_INITIALIZED`: 系统尚未初始化管理员
- `TOO_MANY_ATTEMPTS`: 尝试次数过多，请稍后再试
- `PASSWORD_TOO_SHORT`: 密码长度不得少于 8 位
- `PASSWORD_MISMATCH`: 两次输入的密码不一致
