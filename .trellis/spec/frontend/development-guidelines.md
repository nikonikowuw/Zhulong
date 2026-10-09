# React 前端编码铁律与开发指南

> 基于 `satnaing/shadcn-admin` 的所有者 12 条前端编码铁律、状态流转契约、表格/表单规范与流媒体工业级 UX 指南。

---

## 1. 所有者 12 条编码铁律 (12 Core Rules)

1. **纯函数组件与 React 19 Hooks**：严禁使用 Class 类组件；充分利用 React 19 原生机制与稳定 Hooks。
2. **单一职责与按需拆分**：组件必须专注于单一 UI 职责。当出现独立分区（如复杂配置表单块、多态详情抽屉）时按需拆分子组件，杜绝单个文件超 300 行的生硬单体。
3. **渲染与逻辑彻底分离**：组件只负责声明式 UI 渲染；数据请求、数据拼装与复杂衍生计算必须抽取至自定义 Hook。
4. **插槽组合优先于布尔配置**：优先使用 Radix/shadcn 的组件组合模式（如 `<Dialog><DialogTrigger /><DialogContent /></Dialog>`）或 Slot Props，避免在一个组件上堆砌 10 个布尔 Prop。
5. **TanStack Query 独占服务端状态**：API 数据 100% 由 `@tanstack/react-query` 管理缓存与失效，**严禁将查询数据同步复制进 Zustand 或 Context**；Zustand（如 `auth-store`）仅在跨页面持久化纯客户端状态（如 Token、用户信息）时极度克制地使用。
6. **TanStack Router 强类型文件路由**：
   - 新增页面必须在 `src/routes/` 建立对应文件由插件自动注册；
   - 路由跳转必须使用 `<Link to="..." />` 或 `useNavigate()` 实现类型安全跳转；
   - 页面 URL 参数必须使用 `validateSearch` 配合 Zod Schema 严格校验并提供默认兜底。
7. **数据表格与 URL 状态深度同步**：
   - 所有后台数据列表必须使用 `@tanstack/react-table` 配合 `@/hooks/use-table-url-state`；
   - 当前页码（`page`）、分页大小（`pageSize`）、排序（`sort`）与检索关键词（`search`/`filter`）必须同步至 URL Search Params，保证页面刷新、前进后退及链接分享时状态不丢失。
8. **表单与弹窗统一交互契约**：
   - 表单强制统一使用 `react-hook-form` + `@hookform/resolvers/zod` + `@/components/ui/form`，统一错误提示与焦点捕获，严禁手写繁琐的 `useState` 表单；
   - 对话框与抽屉的状态统一使用 `@/hooks/use-dialog-state`，避免组件间层层透传冗余的 `isOpen / setIsOpen`。
9. **状态就近与渲染派生**：衍生数据直接在渲染期间计算，**严禁使用 `useEffect` 监听源数据并调用 `setState`**。
10. **严格零 `any`**：TypeScript Strict 开启；ESLint 拦截所有显式 `any`；外部不可信网络输入统一标记为 `unknown` 并通过 Zod Schema 强类型解析。
11. **列表 Key 必须业务全局唯一**：循环渲染必须使用后端全局唯一业务 ID（如 `key={camera.id}`），**严禁使用数组索引 `index` 作为 key**。
12. **异步交互必须处理三态**：API 交互必须完备处理并展示：**Loading（骨架屏/加载指示）**、**Error（错误提示与重试机制）** 与 **Empty（空数据引导/清除筛选引导）**。

---

## 2. 表格与表单落地模式 (Table & Form Patterns)

### 2.1 表格 URL 状态驱动范式

```typescript
// features/camera/components/CameraTable.tsx
import { useTableUrlState } from '@/hooks/use-table-url-state'
import {
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
} from '@tanstack/react-table'

export function CameraTable({ data, totalCount }: CameraTableProps) {
  // 分页与筛选双向绑定到 URL
  const { page, pageSize, search, setPage, setPageSize, setSearch } =
    useTableUrlState({
      defaultPage: 1,
      defaultPageSize: 10,
    })

  const table = useReactTable({
    data,
    columns,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    pageCount: Math.ceil(totalCount / pageSize),
  })

  // 渲染 @/components/data-table/ 下的通用 toolbar 与 pagination
  return (
    <div className='space-y-4'>
      <DataTableToolbar table={table} search={search} onSearch={setSearch} />
      <div className='rounded-md border border-border'>
        <Table>{/* 表格内容 */}</Table>
      </div>
      <DataTablePagination
        table={table}
        page={page}
        pageSize={pageSize}
        onPageChange={setPage}
        onPageSizeChange={setPageSize}
      />
    </div>
  )
}
```

### 2.2 表单与 Zod 校验范式

```typescript
// features/camera/components/CameraCreateDialog.tsx
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'

const cameraFormSchema = z.object({
  name: z.string().min(1, '请输入摄像机名称').max(64),
  rtspUrl: z.string().url('请输入合法的 RTSP/HTTP 流地址'),
})

type CameraFormValues = z.infer<typeof cameraFormSchema>

export function CameraCreateForm({ onSubmit, isPending }: Props) {
  const form = useForm<CameraFormValues>({
    resolver: zodResolver(cameraFormSchema),
    defaultValues: { name: '', rtspUrl: '' },
  })

  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
        <FormField
          control={form.control}
          name='name'
          render={({ field }) => (
            <FormItem>
              <FormLabel>摄像机名称</FormLabel>
              <FormControl>
                <Input placeholder='例如：东大门枪机' {...field} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <Button type='submit' disabled={isPending} className='w-full'>
          {isPending ? '保存中...' : '确认添加'}
        </Button>
      </form>
    </Form>
  )
}
```

---

## 3. 实时视频监控与播放器工业级 UX (Live Surveillance & Media UX)

1. **WebCodecs 帧渲染节流与显存释放**：
   - 原生解码帧通过 Canvas 绘制时必须由 `requestAnimationFrame` 调度；
   - 若前一解码帧尚未被画布消耗即迎来新帧，必须在抛弃前显式调用 `oldFrame.close()` 释放底层 GPU 显存纹理；
   - 避免在每帧 `onFrame` 回调中触发高频 React `setState`，仅在分辨率变更、编码格式切换或异常中断时派发状态。
2. **全局快捷键防误触守卫**：
   - 监控中心快捷键（如 `1` / `4` / `9` 分屏矩阵切换、`Esc` 退出全屏、`Space` 轮巡暂停）必须判定 `document.activeElement`；
   - 当用户处于 `input`, `textarea`, `select` 等聚焦输入状态，或处于弹窗打开态时，必须静默屏蔽全局快捷键。
3. **列表客户端筛选与双态空提示**：
   - 必须明确区分两类空状态：
     - **资产空状态（Initial Empty）**：系统尚未添加任何设备，展示插画与「添加第一台设备」主行动点；
     - **筛选空状态（Filter Empty）**：当前搜索关键词无匹配项，展示「未找到匹配项」并提供「重置筛选」按钮。

---

## 4. 控制台架构与流式大视口规范 (Console Architecture)

1. **统一采用 `AuthenticatedLayout` 框架**：
   - 页面结构为：**`SidebarProvider` + `AppSidebar` + `SidebarInset` + `Header` + `Main`**；
   - 侧边栏支持默认记忆展开/折叠状态（通过 `getCookie('sidebar_state')` 持久化），并在移动端自动降级为响应式抽屉。
2. **全宽流式工作区 (`@container/content`)**：
   - 工作区宽度应占满水平视口，禁止使用限定 1200px 最大宽度的营销页居中容器；
   - 为监控矩阵与设备拓扑保留完整的视口高度（支持 `has-data-[layout=fixed]:h-svh` 满高视口排布）。
3. **快捷命令搜寻集成 (Command Menu)**：
   - 全局保留 `Ctrl+K`（或 `Cmd+K`）调起 `CommandMenu`，支持快速跳转至指定功能模块与摄像机频道。
