import { format } from 'date-fns'
import { useMutation } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatLocalDateTime, toCsv, triggerDownload } from '@/lib/csv'
import { auditApi } from '../api/audit-api'
import { type AuditLog, type GetAuditLogsParams } from '../data/schema'

/**
 * 单页请求行数。
 *
 * 后端 `httputil.MaxPageSize` 会把更大的 `pageSize` 静默 clamp 到 100，
 * 请求 5000 只会拿到 100 行却回显 100 —— 导出必须按服务端上限自己翻页。
 */
const EXPORT_PAGE_SIZE = 100

/**
 * 单次导出的行数硬上限，与审计表滚动上限（`audit.DefaultMaxEntries = 5000`）对齐。
 * 触顶时只导出最新的一批并在 toast 里说明，避免一次点击拉出无界流量。
 */
const MAX_EXPORT_ROWS = 5000

interface AuditExportPayload {
  logs: AuditLog[]
  total: number
}

/**
 * 按服务端上限翻页拉取全部命中记录。
 *
 * 三条退出条件缺一不可：空页（`total` 虚高时防死循环）、已取满 `total`、触顶硬上限。
 */
async function fetchExportLogs(
  filter: GetAuditLogsParams
): Promise<AuditExportPayload> {
  const logs: AuditLog[] = []

  for (let page = 1; ; page += 1) {
    const res = await auditApi.getAuditLogs({
      ...filter,
      page,
      pageSize: EXPORT_PAGE_SIZE,
    })
    logs.push(...res.items)

    if (
      res.items.length === 0 ||
      logs.length >= res.total ||
      logs.length >= MAX_EXPORT_ROWS
    ) {
      return { logs, total: res.total }
    }
  }
}

/**
 * 审计日志 CSV 导出。
 *
 * 走 `useMutation` 而不是组件内裸 `await`：pending 态与错误提示收敛在一个入口，
 * 并沿用全局 mutation 约定（此处抑制通用提示，改用导出的专用文案）。
 */
export function useAuditExport() {
  const { t } = useTranslation('audit')

  const mutation = useMutation({
    mutationFn: (filter: GetAuditLogsParams) => fetchExportLogs(filter),
    meta: { suppressGlobalToast: true },
    onSuccess: ({ logs, total }) => {
      if (logs.length === 0) {
        toast.info(t('toast.exportEmpty'))
        return
      }

      const headers = [
        t('table.columns.id'),
        t('table.columns.createdAt'),
        t('table.columns.username'),
        t('table.columns.action'),
        t('table.columns.target'),
        t('table.columns.ip'),
        t('table.columns.status'),
        t('csv.detailColumn'),
      ]

      const rows = logs.map((log) => [
        log.id,
        // 与列表、records 导出同口径：CSV 是给人看的报表，写本地时区
        formatLocalDateTime(log.createdAt),
        log.username,
        t(`actionNames.${log.action}`, { defaultValue: log.action }),
        log.target,
        log.ip,
        t(`status.${log.status}`, { defaultValue: log.status }),
        log.detail,
      ])

      triggerDownload(
        new Blob([toCsv(headers, rows)], { type: 'text/csv;charset=utf-8;' }),
        `zhulong_audit_logs_${format(new Date(), 'yyyyMMdd_HHmmss')}.csv`
      )

      if (logs.length < total) {
        toast.warning(t('toast.exportTruncated', { count: logs.length, total }))
        return
      }
      toast.success(t('toast.exportSuccess', { count: logs.length }))
    },
    onError: (err) => {
      toast.error(t('toast.exportFailed', { error: err.message }))
    },
  })

  return {
    exportLogs: (filter: GetAuditLogsParams) => mutation.mutate(filter),
    isExporting: mutation.isPending,
  }
}
