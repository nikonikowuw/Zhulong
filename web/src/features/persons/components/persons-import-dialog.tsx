import { useMemo, useRef, useState } from 'react'
import { type TFunction } from 'i18next'
import {
  AlertCircle,
  CheckCircle2,
  Download,
  FileDown,
  Loader2,
  Upload,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { toCsv, triggerDownload } from '@/lib/csv'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { type PersonImportRow } from '../data/schema'
import {
  useCommitPersonImport,
  usePreviewPersonImport,
} from '../hooks/use-persons'
import { usePersonsActions, usePersonsState } from './persons-provider'

/** CSV 模板与导出共用的列头。 */
const MANIFEST_HEADERS = ['name', 'personId'] as const

function downloadImportTemplate() {
  const csv = toCsv(MANIFEST_HEADERS, [['Alice', 'EMP-001']])
  triggerDownload(
    new Blob([csv], { type: 'text/csv;charset=utf-8' }),
    'person-import-template.csv'
  )
}

/**
 * 导出失败行。
 *
 * 走 `@/lib/csv` 而不是本地一份 `escapeCsvCell`：那里的 `escapeCsvField` 覆盖
 * 了 `\t\r` 前缀（Excel 同样会把它当公式起始）、数值类型豁免加引号，并且
 * `triggerDownload` 会在 10s 后才 revoke —— 立即 revoke 会让 Safari/Firefox
 * 在下载真正启动前丢掉大 Blob。
 */
function downloadFailedRows(rows: PersonImportRow[], filename: string) {
  const csv = toCsv(
    MANIFEST_HEADERS,
    rows.map((row) => [row.name, row.personId ?? ''])
  )
  triggerDownload(new Blob([csv], { type: 'text/csv;charset=utf-8' }), filename)
}

export function PersonsImportDialog() {
  const { t } = useTranslation('persons')
  const { open } = usePersonsState()
  const { setOpen } = usePersonsActions()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const previewMutation = usePreviewPersonImport()
  const commitMutation = useCommitPersonImport()
  const preview = previewMutation.data ?? null
  const result = commitMutation.data ?? null
  const isOpen = open === 'import'
  const isBusy = previewMutation.isPending || commitMutation.isPending

  const failedRows = useMemo(
    () =>
      (result?.items ?? preview?.items ?? []).filter(
        (row) => row.issues.length > 0
      ),
    [result?.items, preview?.items]
  )

  const reset = () => {
    setFile(null)
    previewMutation.reset()
    commitMutation.reset()
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const selectFile = async (selected: File | undefined) => {
    if (!selected) return
    if (!selected.name.toLowerCase().endsWith('.csv')) {
      toast.error(t('import.invalidFile'))
      return
    }
    setFile(selected)
    commitMutation.reset()
    try {
      await previewMutation.mutateAsync(selected)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  const commit = async () => {
    if (!file) return
    try {
      await commitMutation.mutateAsync(file)
      toast.success(t('import.completed'))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) {
          setOpen(null)
          reset()
        }
      }}
    >
      <DialogContent className='sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('import.title')}</DialogTitle>
          <DialogDescription>{t('import.description')}</DialogDescription>
        </DialogHeader>

        {!file ? (
          <div className='space-y-3'>
            <input
              ref={fileInputRef}
              type='file'
              accept='.csv,text/csv'
              className='hidden'
              onChange={(event) => void selectFile(event.target.files?.[0])}
            />
            <div className='grid gap-3 sm:grid-cols-[1fr_auto] sm:items-stretch'>
              <button
                type='button'
                className='flex min-h-32 cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border bg-muted/30 text-sm text-muted-foreground transition-colors hover:bg-muted/50'
                onClick={() => fileInputRef.current?.click()}
              >
                <Upload className='size-6' />
                <span>{t('import.chooseFile')}</span>
                <span className='text-xs'>{t('import.fileHint')}</span>
              </button>
              <Button
                type='button'
                variant='outline'
                className='min-h-16 sm:min-h-32 sm:w-44 sm:flex-col sm:gap-2'
                onClick={downloadImportTemplate}
              >
                <FileDown className='size-5' />
                <span>{t('import.downloadTemplate')}</span>
              </Button>
            </div>
            <div className='rounded-md border border-border bg-muted/20 px-3 py-2 text-xs text-muted-foreground'>
              <div className='font-medium text-foreground'>
                {t('import.formatTitle')}
              </div>
              <code className='mt-1 block overflow-x-auto font-mono whitespace-pre'>
                {t('import.formatExample')}
              </code>
              <div className='mt-1'>{t('import.formatHint')}</div>
            </div>
          </div>
        ) : (
          <div className='space-y-4'>
            <div className='flex items-center justify-between gap-2 rounded-md border border-border bg-muted/30 px-3 py-2 text-sm'>
              <span className='truncate'>{file.name}</span>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                onClick={reset}
                disabled={isBusy}
              >
                {t('import.chooseAnother')}
              </Button>
            </div>

            {isBusy && !preview && (
              <div className='flex items-center gap-2 text-sm text-muted-foreground'>
                <Loader2 className='size-4 animate-spin' />
                {t('import.validating')}
              </div>
            )}

            {preview && !result && (
              <>
                <div className='grid grid-cols-3 gap-2 text-center text-sm'>
                  <div className='rounded-md border border-border bg-muted/30 p-2'>
                    <div className='text-lg font-semibold'>{preview.total}</div>
                    <div className='text-muted-foreground'>
                      {t('import.total')}
                    </div>
                  </div>
                  <div className='rounded-md border border-border bg-muted/30 p-2'>
                    <div className='text-lg font-semibold'>
                      {preview.importable}
                    </div>
                    <div className='text-muted-foreground'>
                      {t('import.importable')}
                    </div>
                  </div>
                  <div className='rounded-md border border-border bg-muted/30 p-2'>
                    <div className='text-lg font-semibold'>
                      {preview.failed}
                    </div>
                    <div className='text-muted-foreground'>
                      {t('import.failed')}
                    </div>
                  </div>
                </div>
                <ImportRowsTable rows={preview.items} t={t} />
              </>
            )}

            {result && (
              <>
                <Alert>
                  <CheckCircle2 />
                  <AlertTitle>{t('import.completed')}</AlertTitle>
                  <AlertDescription>
                    {t('import.resultSummary', {
                      imported: result.imported,
                      failed: result.failed,
                    })}
                  </AlertDescription>
                </Alert>
                <ImportRowsTable rows={result.items} t={t} isResult />
              </>
            )}

            {(previewMutation.isError || commitMutation.isError) && (
              <Alert variant='destructive'>
                <AlertCircle />
                <AlertTitle>{t('import.failedTitle')}</AlertTitle>
                <AlertDescription>{t('import.retryHint')}</AlertDescription>
              </Alert>
            )}
          </div>
        )}

        <DialogFooter>
          {failedRows.length > 0 && (
            <Button
              type='button'
              variant='outline'
              onClick={() =>
                downloadFailedRows(failedRows, 'person-import-failed.csv')
              }
            >
              <Download className='me-1 size-4' />
              {t('import.downloadFailed')}
            </Button>
          )}
          <Button
            type='button'
            variant='outline'
            onClick={() => {
              setOpen(null)
              reset()
            }}
            disabled={isBusy}
          >
            {t('actions.cancel')}
          </Button>
          {preview && !result && preview.importable > 0 && (
            <Button
              type='button'
              onClick={() => void commit()}
              disabled={isBusy}
            >
              {isBusy && <Loader2 className='me-1 size-4 animate-spin' />}
              {t('import.confirm')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ImportRowsTable({
  rows,
  t,
  isResult = false,
}: {
  rows: PersonImportRow[]
  t: TFunction
  isResult?: boolean
}) {
  return (
    <ScrollArea className='h-64 rounded-md border border-border'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('import.line')}</TableHead>
            <TableHead>{t('fields.name')}</TableHead>
            <TableHead>{t('fields.personId')}</TableHead>
            <TableHead>{t('import.status')}</TableHead>
            <TableHead>{t('import.reason')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => {
            const hasIssues = row.issues.length > 0
            const statusText = hasIssues
              ? t('import.invalid')
              : isResult && row.imported
                ? t('import.imported')
                : t('import.ready')

            return (
              <TableRow key={row.line}>
                <TableCell>{row.line}</TableCell>
                <TableCell>{row.name || '-'}</TableCell>
                <TableCell>{row.personId || '-'}</TableCell>
                <TableCell>{statusText}</TableCell>
                <TableCell className='whitespace-normal text-destructive'>
                  {row.issues
                    .map((issue) => t(`import.errors.${issue.code}`))
                    .join('、') || '-'}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </ScrollArea>
  )
}
