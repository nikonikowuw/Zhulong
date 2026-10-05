# 实施计划：项目全栈骨架

## 实施顺序

1. **冻结文档合同**
   - 保持产品 PRD 与 backend HTTP/error 规范对齐：`code/message/data` 为必需字段；错误 `data` 为 `null`；只有 HTTP 422 字段校验错误可附加本地化 `details`。
   - 保留 `.trellis/config.yaml`、`AGENTS.md`、`docs/`、`go.mod` 中已有的用户改动；只在原内容上增量修改。

2. **建立 native 库边界**
   - 新增 CMake 工程、公开纯 C 不透明句柄头文件、C++17 stub 和 CTest 生命周期测试。
   - 验证异常隔离、状态码、生命周期转换和销毁行为，不依赖硬件。

3. **实现 Go native bridge 与宿主**
   - 新增 CGO wrapper，并测试 C-owned handle 的创建、启动、停止、幂等 Close 与并发访问保护。
   - 建立配置、Zap、Gin 路由、统一响应/错误 helper、Swaggo 文档生成、GORM/SQLite 与嵌入式版本化迁移。
   - 实现 Fx 生命周期协调器：迁移先于 HTTP、部分启动逆序回滚、优雅关闭和日志 Sync。
   - 测试健康响应、API 未命中隔离、迁移失败阻止监听和正常关闭顺序。

4. **实现 React shell 和 Go embed**
   - 新增 `web/`，配置严格 TypeScript、Vite、React Router、TanStack Query、Tailwind 语义 token，以及 `en`/`zh-Hans`/`zh-Hant` 的 `react-i18next` 资源。
   - 实现本地化状态 shell，按系统偏好初始化主题、按浏览器偏好初始化语言；不实现摄像机、任务、回放或算法工作流。
   - Vite 输出进入 Go embed 包，增加 Vitest/RTL 覆盖。

5. **建立根级开发和验证入口**
   - 新增 Makefile targets：依赖安装、前端 build/check、native build/test、Go build/test/vet、run 和构建后二进制 smoke test。
   - 更新 README，说明主机依赖、干净检出的命令、数据目录行为及尚无硬件支持的边界。
   - 仅为生成构建输出、node_modules 和本机数据添加忽略规则。

6. **跨层验证与最终复核**
   - 运行格式化、Go vet/race tests、CTest、前端 lint/type-check/tests/build、全量根级 build 和二进制 smoke tests。
   - 复核 API 响应合同、C ABI 所有权/异常安全、启动回滚及用户原有文件差异。

## 验证命令

Makefile 最终提供稳定的根级入口，规划命令为：

```bash
make check
make build
make smoke
```

`make check` 先准备 native 和 embed 依赖，再执行 CMake/CTest、`go vet ./cmd/... ./internal/...`、`go test -race ./cmd/... ./internal/...`、前端 lint/type-check/Vitest/build。`make smoke` 使用临时数据目录启动生成的可执行文件，检查 `/api/v1/health`、`/` 和未匹配 `/api/...` 路由。

## 风险文件与边界

- `internal/engine/*` 和 `native/include/Zhulong/engine.h`：CGO 指针规则、C-owned handle 生命周期、异常边界，以及 Darwin/Linux C++ runtime 链接。
- `internal/app/*` 和 `internal/database/*`：迁移失败必须阻止 listener；后续启动失败要释放先前已打开资源。
- `internal/webui/*`、`web/vite.config.*` 和 Makefile：embed pattern 需要资源目录存在，build 顺序必须显式。
- `docs/prd/prd-v1.0.md` 与 `.trellis/spec/backend/*`：字段级 `details` 只用于 HTTP 422，错误时 `data` 固定 `null`，不回显原始输入。
- `.trellis/config.yaml`、`AGENTS.md` 和 `go.mod` 有用户未提交内容；保留它们并在必要处增量编辑。

## 回滚点

- 若 native 静态库无法在支持的本机工具链稳定链接，先停止该层实现并修订设计；不能静默改成独立进程或其他 ABI 架构。
- 若 Go embed 在干净检出失败，修正 sentinel 与构建依赖，不提交不透明生成产物来掩盖问题。
- 若迁移或前端工具和现有工具链不兼容，限定在该层记录证据并回到设计阶段，不自行扩大产品范围。
