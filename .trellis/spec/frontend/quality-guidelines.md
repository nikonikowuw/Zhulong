# React 前端质量门禁与测试规范

> 前端组件/逻辑测试（Vitest + RTL）、TypeScript 强类型检测及内嵌资源构建。

---

## 1. 质量检验命令与门禁清单

```bash
npm run lint         # 静态风格与禁止 any 检查
npm run type-check   # tsc --noEmit 强类型校验
npm run test         # Vitest 单元与交互测试
npm run build        # Vite 生产构建 (验证无告警并输出至 Go 嵌入目录)
```

---

## 2. 自动化测试规范 (Vitest + RTL)

- **面向用户行为**：通过 `screen.getByRole`、`screen.getByText` 查询元素并用 `userEvent` 触发交互；严禁测试私有状态或 Hook 实现细节。
- **三态全覆盖**：涉及 API 调用的组件测试，必须覆盖：
  1. 加载中骨架屏展示；
  2. 正常数据列表渲染；
  3. 请求失败时错误提示条与重试按钮交互。

---

## 3. 严格类型约束与零 any 审计

1. **`@typescript-eslint/no-explicit-any: "error"`**：CI 和本地构建中任何显式 `any` 直接报错阻断。
2. **外部不可信数据处理**：网络输入与 LocalStorage 读取的数据标注为 `unknown`，必须经 **Zod Schema 校验解析** 为强类型对象后方可使用。

---

## 4. Vite 打包与 Go 二进制嵌入对接

- **构建输出路径**：Vite 的 `outDir` 配置为输出至 `../internal/webui/dist`。
- **打包要点**：
  - 产物不得引用本地绝对路径；
  - 静态资源使用相对路径，确保 Go `embed.FS` 正确路由；
  - 构建产物必须包含 3 种语言的完整翻译字典；
  - Vite `emptyOutDir` 会清理输出目录；`preserve-go-embed-target` 插件在 build 完成后重建跟踪的 `.keep`，保证干净检出或清理后的目录仍满足 `//go:embed all:dist`；
  - 根级 `make build` 必须先构建前端，再编译 CMake native 库和 Go 主程序。直接运行 `go build` 不是完整交付构建入口。
