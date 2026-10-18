import { HelpCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { FormLabel, FormMessage } from '@/components/ui/form'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

interface AlgoConfigFieldLabelProps {
  title: string
  propertyKey: string
  description?: string
}

/** 字段标题 + 原始键名 + 说明 Tooltip + 校验信息，供全部字段类型复用。 */
export function AlgoConfigFieldLabel({
  title,
  propertyKey,
  description,
}: AlgoConfigFieldLabelProps) {
  return (
    <div className='min-w-0 space-y-0.5'>
      <div className='flex min-w-0 items-center gap-1.5'>
        <FormLabel className='cursor-pointer truncate text-xs font-medium text-foreground'>
          {title}
        </FormLabel>
        {title !== propertyKey && (
          <span className='font-mono text-[10px] text-muted-foreground/60'>
            ({propertyKey})
          </span>
        )}
        {description && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type='button'
                variant='ghost'
                size='icon'
                aria-label={description}
                className='size-4 shrink-0 text-muted-foreground/60 hover:bg-transparent hover:text-foreground'
              >
                <HelpCircle className='h-3 w-3' />
              </Button>
            </TooltipTrigger>
            <TooltipContent side='top' className='max-w-xs text-xs'>
              {description}
            </TooltipContent>
          </Tooltip>
        )}
      </div>
      <FormMessage className='text-[10px]' />
    </div>
  )
}
