import z from 'zod'

/** 后端 `faceStatus` 只接受单值（enrolled / missing）。 */
const faceStatusFilterSchema = z.enum(['enrolled', 'missing'])

/**
 * 人员页的 URL 搜索参数契约。
 *
 * 单独成模块而不是内联在 `routes/` 里，是为了能被单测直接覆盖 —— 这里的
 * 边界夹紧与形状归一化是整个列表页不会被手改 URL 打崩的前提。
 *
 * ⚠️ 只写 `.optional().catch()` 是不够的：`0` 是合法 number，`catch` 不会介入，
 * 于是 `Math.ceil(total / 0)` 变成 `Infinity`，分页条渲染出「共 Infinity 页」。
 * 上限与 `httputil.MaxPageSize` / `httputil.MaxPage` 对齐。
 */
export const personsSearchSchema = z.object({
  view: z.enum(['table', 'grid']).optional().catch('table'),
  page: z.number().int().min(1).max(100000).optional().catch(1),
  pageSize: z.number().int().min(1).max(100).optional().catch(10),
  search: z.string().optional().catch(''),
  personId: z.string().optional().catch(undefined),
  /**
   * 底库筛选。**对外形状必须是单值字符串**，不能归一化成数组：
   * `useTableUrlState({ type: 'string' })` 按字符串读取该键，数组会让它读不到
   * 值 —— 筛选器永远显示未选中，而请求其实已经带上过滤，来回一改就静默失效。
   *
   * 仍然接受重复 query key（`?faceStatus=enrolled&faceStatus=missing`，旧链接
   * 或 faceted filter 的历史形态），只取首个值以匹配后端单值契约。
   */
  faceStatus: z
    .union([z.array(faceStatusFilterSchema), faceStatusFilterSchema])
    .optional()
    .catch(undefined)
    .transform((val) => (Array.isArray(val) ? val[0] : val)),
})

export type PersonsSearch = z.infer<typeof personsSearchSchema>
