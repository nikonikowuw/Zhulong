/**
 * 后台导出进度卡的公开入口。
 *
 * 不并入 `index.tsx`：那是记录页的**页面**入口，静态依赖整棵页面组件树；
 * 全局布局只需要这张几 KB 的浮层，走页面 barrel 会把 records 页面 chunk
 * 拖进首屏。这里只转出浮层本身，导出流水线（fflate/zod）仍是按需 `import()`。
 */
export { RecordsExportJobIndicator } from './components/export/records-export-job-indicator'
