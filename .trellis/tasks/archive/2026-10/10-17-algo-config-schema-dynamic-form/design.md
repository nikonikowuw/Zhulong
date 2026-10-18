# Technical Design: 算法配置 Schema 动态表单组件 (Dynamic Algorithm Config Form)

## 1. 架构定位与组件拓扑

本方案在保持已有领域模型与 API 契约（`configJson: string`）完全不变的前提下，在 `web/src/features/tasks/components` 中落地一个高聚合、双模态的参数编辑组件：

```txt
┌────────────────────────────────────────────────────────────────────────┐
│ ChannelTaskDialog (通道多算法配置弹窗)                                     │
│   │                                                                    │
│   └─► AlgoConfigDynamicForm (双模态配置器)                              │
│         ├─ Header 控制栏: [表单模式 | JSON 源码模式] 切换 + [重置默认值]     │
│         │                                                              │
│         ├─ 模式 A: 动态表单视图 (Dynamic Form View)                      │
│         │     ├─ Slider + NumberInput (带范围的浮点/整数，如置信度阈值)    │
│         │     ├─ Switch (布尔开关)                                     │
│         │     ├─ Select (枚举项下拉)                                   │
│         │     └─ Input (字符串/通用数字)                                │
│         │                                                              │
│         ├─ 模式 B: JSON 源码视图 (Raw JSON View)                         │
│         │     └─ Textarea + 格式化按钮 + 实时语法检测                    │
│         │                                                              │
│         └─ 模式 C: 零参空状态 (Zero Config View)                         │
│               └─ 友好提示 "该算法无需额外参数配置"                         │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 状态流转与双向同步机制

组件接收父级传入的 `value: string`（即当前保存的 `configJson`）与 `schemaJson: string`（算法包声明的 JSON Schema）。

### 2.1 状态模型
```typescript
interface AlgoConfigDynamicFormProps {
  schemaJson: string
  value: string
  onChange: (nextJson: string) => void
  disabled?: boolean
}
```

内部维护：
1. `mode: 'form' | 'json'`：当前展现模式。当 Schema 无法解析或无 properties 时，优雅退化至仅提示无参或 JSON 模式；
2. `parsedValues: Record<string, any>`：表单态下的解析对象，由 `value` 派生反序列化；
3. `jsonError: string | null`：JSON 源码模式下的即时语法校验错误。

### 2.2 双向同步与切模守卫
1. **表单 $\rightarrow$ JSON**：
   * 用户在表单中拖动 Slider、切换 Switch 或输入内容时，更新 `parsedValues` 对应字段；
   * 自动序列化为 `JSON.stringify(nextValues, null, 2)` 并触发父级 `onChange`。
2. **JSON $\rightarrow$ 表单**：
   * 用户在 JSON 文本框中编辑内容时，直接触发 `onChange(rawText)`；
   * 当用户尝试点击「切换至表单模式」时，执行 `JSON.parse(rawText)`：
     * 若解析成功：无缝切入表单模式，字段值立即回显；
     * 若解析失败：Toast 报错并阻止切模，提示用户先修正 JSON 语法。
3. **初次加载与默认值种子填充**：
   * 若初始传入的 `value` 为空或 `'{}'`，且 Schema 定义了 `default` 字段，初次挂载时自动提取默认值并执行一次平滑种子填充。
4. **一键重置默认值**：
   * 提取 Schema 中所有字段的 `default` 值，重新构建 JSON 并通知父级。

---

## 3. 字段类型推导与组件映射策略

根据 JSON Schema Draft-07 规范字段进行模式匹配：

```typescript
export interface SchemaProperty {
  type?: 'string' | 'number' | 'integer' | 'boolean' | 'array' | 'object'
  title?: string
  description?: string
  default?: any
  minimum?: number
  maximum?: number
  step?: number
  enum?: (string | number)[]
}
```

推导逻辑优先级：
1. `enum` 有值 $\rightarrow$ 渲染为 `<Select>`；
2. `type === 'boolean'` $\rightarrow$ 渲染为 `<Switch>`；
3. `type === 'number' | 'integer'`：
   * 若定义了 `minimum !== undefined && maximum !== undefined` $\rightarrow$ 渲染为 `<Slider>` + `<Input type="number">` 双向联动；
   * 否则 $\rightarrow$ 渲染为标准 `<Input type="number">`；
4. `type === 'string'` $\rightarrow$ 渲染为标准 `<Input type="text">`；
5. 其他未覆盖的复杂结构（如嵌套对象/数组） $\rightarrow$ 降级为在该项下方提示或引导至 JSON 模式。

---

## 4. UI 视觉与无障碍表现 (Apple-Style)

* **紧凑表单卡片**：参数配置区置于每个已勾选算法卡片的内部，采用细边框 `rounded-lg bg-background/50 p-3`；
* **Header 控制区**：
  * 左侧：小尺寸 Segmented 切换标签（`[ 表单 | JSON ]`）；
  * 右侧：幽灵按钮「重置默认值」与「格式化」（JSON 模式可见）；
* **Slider 交互体验**：
  * 采用双栏微布局：左侧 Slider 滑动条，右侧固定宽度（`w-16`）数字输入框，数值实时同步，拖动时提供即时触觉般平滑反馈；
* **次级说明与 Tooltip**：
  * 若 Schema 声明了 `description`，在 Label 旁边放置小号 `HelpCircle` 图标，悬浮展开 Tooltip 说明，不侵占界面垂直空间。

---

## 5. 模块改造步骤

1. **新建组件**：
   * `web/src/features/tasks/components/algo-config-dynamic-form.tsx`
2. **多语言词条**：
   * 更新 `web/src/features/tasks/locales/{en,zh-Hans,zh-Hant}.json`，补充动态表单相关文案（表单模式、JSON 模式、重置默认值、零参提示、语法错误提示等）；
3. **集成改造**：
   * 在 `ChannelTaskDialog` 中引入 `AlgoConfigDynamicForm`，替换原有裸 `Textarea`；
4. **测试套件**：
   * 编写 `web/src/features/tasks/components/algo-config-dynamic-form.test.tsx`，覆盖各种字段、切模验证与容错；
   * 补充 `ChannelTaskDialog` 相关联动测试。
