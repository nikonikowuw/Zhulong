import { useEffect, useRef, useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/clipboard'
import { statusTextClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

export interface TaskCopyIdButtonProps {
  id: string
  /** `task` = 通道任务 ID，`subscription` = 算法订阅（`task_algorithms.id`）。 */
  label: 'task' | 'subscription'
  className?: string
}

/**
 * 单条 ID 的内联复制控件（非安全上下文下由共享工具回退）。
 *
 * 通道表格主行与二级明细表共用同一实现——此前两处各写了一份几乎逐字相同的控件。
 */
export function TaskCopyIdButton({
  id,
  label,
  className,
}: TaskCopyIdButtonProps): React.JSX.Element {
  const { t } = useTranslation('tasks')
  const [copied, setCopied] = useState(false)
  const copyTimerRef = useRef<number | null>(null)

  useEffect(
    () => () => {
      if (copyTimerRef.current !== null) {
        window.clearTimeout(copyTimerRef.current)
      }
    },
    []
  )

  const text =
    label === 'task'
      ? {
          aria: t('table.copyTaskId', '复制任务 ID'),
          success: t('table.copyTaskIdSuccess', '任务 ID 已复制到剪贴板'),
        }
      : {
          aria: t('table.copySubscriptionId', '复制订阅 ID'),
          success: t(
            'table.copySubscriptionIdSuccess',
            '订阅 ID 已复制到剪贴板'
          ),
        }

  const handleCopy = async (event: React.MouseEvent) => {
    event.stopPropagation()
    if (!(await copyToClipboard(id))) {
      toast.error(t('table.copyIdFailed', '复制失败，请手动复制'))
      return
    }
    setCopied(true)
    toast.success(text.success)
    if (copyTimerRef.current !== null) {
      window.clearTimeout(copyTimerRef.current)
    }
    copyTimerRef.current = window.setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Button
      variant='ghost'
      size='icon'
      className={cn(
        'size-6 shrink-0 text-muted-foreground opacity-70 transition-opacity hover:text-foreground hover:opacity-100',
        className
      )}
      aria-label={text.aria}
      title={text.aria}
      onClick={handleCopy}
    >
      {copied ? (
        <Check className={cn('size-3.5', statusTextClass('active'))} />
      ) : (
        <Copy className='size-3.5' />
      )}
    </Button>
  )
}
