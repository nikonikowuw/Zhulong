import { type TFunction } from 'i18next'
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

interface PersonsImportPreviewTableProps {
  rows: PersonImportRow[]
  t: TFunction
  isResult?: boolean
}

export function PersonsImportPreviewTable({
  rows,
  t,
  isResult = false,
}: PersonsImportPreviewTableProps) {
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
                    // 后端新增错误码时 `import.errors.*` 可能还没跟上：
                    // 必须回落到稳定机器码，而不是把这串原始 i18n key
                    // 直接扇给用户。
                    .map((issue) =>
                      t(`import.errors.${issue.code}`, {
                        defaultValue: issue.code,
                      })
                    )
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
