import { z } from 'zod'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { fonts } from '@/config/fonts'
import { ChevronDown, Moon, Sun, Type } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { useFont } from '@/context/font-provider'
import { useTheme } from '@/context/theme-provider'
import { Button, buttonVariants } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'

const appearanceFormSchema = z.object({
  theme: z.enum(['light', 'dark']),
  font: z.enum(fonts),
})

type AppearanceFormValues = z.infer<typeof appearanceFormSchema>

export function AppearanceForm() {
  const { t } = useTranslation()
  const { font, setFont } = useFont()
  const { theme, setTheme } = useTheme()

  const defaultValues: Partial<AppearanceFormValues> = {
    theme: (theme === 'dark' ? 'dark' : 'light') as 'light' | 'dark',
    font,
  }

  const form = useForm<AppearanceFormValues>({
    resolver: zodResolver(appearanceFormSchema),
    defaultValues,
  })

  const watchedFont = useWatch({ control: form.control, name: 'font' }) || font

  function onSubmit(data: AppearanceFormValues) {
    if (data.font !== font) setFont(data.font)
    if (data.theme !== theme) setTheme(data.theme)

    toast.success(
      t('appearance.saveSuccess', {
        defaultValue: '外观偏好设置已更新并生效',
      })
    )
  }

  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
        {/* Card 1: Theme Mode Selection */}
        <Card className='border-border/60 bg-card/60 backdrop-blur-sm'>
          <CardHeader className='pb-4'>
            <CardTitle className='text-base font-semibold'>
              {t('appearance.themeTitle', { defaultValue: '主题模式' })}
            </CardTitle>
            <CardDescription>
              {t('appearance.themeDesc', {
                defaultValue:
                  '选择控制台界面的明暗风格，暗色模式推荐在机房或夜间监控环境中使用。',
              })}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <FormField
              control={form.control}
              name='theme'
              render={({ field }) => (
                <FormItem className='space-y-1'>
                  <FormMessage />
                  <RadioGroup
                    onValueChange={field.onChange}
                    defaultValue={field.value}
                    className='grid max-w-2xl grid-cols-1 gap-6 sm:grid-cols-2'
                  >
                    {/* Light Theme Option */}
                    <FormItem>
                      <FormLabel className='cursor-pointer font-normal [&:has([data-state=checked])>div]:border-primary [&:has([data-state=checked])>div]:ring-2 [&:has([data-state=checked])>div]:ring-primary/20'>
                        <FormControl>
                          <RadioGroupItem value='light' className='sr-only' />
                        </FormControl>
                        <div className='flex flex-col gap-2 rounded-lg border-2 border-muted bg-card p-2 transition-all hover:border-accent'>
                          <div className='space-y-2 rounded-md bg-[#ecedef] p-3'>
                            <div className='space-y-1.5 rounded-md bg-white p-2.5 shadow-xs'>
                              <div className='h-2.5 w-20 rounded-md bg-[#ecedef]' />
                              <div className='h-2.5 w-28 rounded-md bg-[#ecedef]' />
                            </div>
                            <div className='flex items-center space-x-2 rounded-md bg-white p-2 shadow-xs'>
                              <div className='h-4 w-4 rounded-full bg-[#ecedef]' />
                              <div className='h-2.5 w-24 rounded-md bg-[#ecedef]' />
                            </div>
                            <div className='flex items-center space-x-2 rounded-md bg-white p-2 shadow-xs'>
                              <div className='h-4 w-4 rounded-full bg-[#ecedef]' />
                              <div className='h-2.5 w-20 rounded-md bg-[#ecedef]' />
                            </div>
                          </div>
                          <div className='flex items-center justify-between px-2 py-1'>
                            <div className='flex items-center gap-2'>
                              <Sun className='h-4 w-4 text-amber-500' />
                              <span className='text-sm font-medium'>
                                {t('appearance.light', {
                                  defaultValue: '浅色明亮 (Light)',
                                })}
                              </span>
                            </div>
                            <span className='text-xs text-muted-foreground'>
                              {t('appearance.lightHint', {
                                defaultValue: '日间环境',
                              })}
                            </span>
                          </div>
                        </div>
                      </FormLabel>
                    </FormItem>

                    {/* Dark Theme Option */}
                    <FormItem>
                      <FormLabel className='cursor-pointer font-normal [&:has([data-state=checked])>div]:border-primary [&:has([data-state=checked])>div]:ring-2 [&:has([data-state=checked])>div]:ring-primary/20'>
                        <FormControl>
                          <RadioGroupItem value='dark' className='sr-only' />
                        </FormControl>
                        <div className='flex flex-col gap-2 rounded-lg border-2 border-muted bg-card p-2 transition-all hover:border-accent'>
                          <div className='space-y-2 rounded-md bg-slate-950 p-3'>
                            <div className='space-y-1.5 rounded-md bg-slate-800 p-2.5 shadow-xs'>
                              <div className='h-2.5 w-20 rounded-md bg-slate-600' />
                              <div className='h-2.5 w-28 rounded-md bg-slate-600' />
                            </div>
                            <div className='flex items-center space-x-2 rounded-md bg-slate-800 p-2 shadow-xs'>
                              <div className='h-4 w-4 rounded-full bg-slate-600' />
                              <div className='h-2.5 w-24 rounded-md bg-slate-600' />
                            </div>
                            <div className='flex items-center space-x-2 rounded-md bg-slate-800 p-2 shadow-xs'>
                              <div className='h-4 w-4 rounded-full bg-slate-600' />
                              <div className='h-2.5 w-20 rounded-md bg-slate-600' />
                            </div>
                          </div>
                          <div className='flex items-center justify-between px-2 py-1'>
                            <div className='flex items-center gap-2'>
                              <Moon className='h-4 w-4 text-primary' />
                              <span className='text-sm font-medium'>
                                {t('appearance.dark', {
                                  defaultValue: '暗色深邃 (Dark)',
                                })}
                              </span>
                            </div>
                            <span className='text-xs text-muted-foreground'>
                              {t('appearance.darkHint', {
                                defaultValue: '机房暗光',
                              })}
                            </span>
                          </div>
                        </div>
                      </FormLabel>
                    </FormItem>
                  </RadioGroup>
                </FormItem>
              )}
            />
          </CardContent>
        </Card>

        {/* Card 2: Font Preference & Live Typographic Preview */}
        <Card className='border-border/60 bg-card/60 backdrop-blur-sm'>
          <CardHeader className='pb-4'>
            <CardTitle className='text-base font-semibold'>
              {t('appearance.fontTitle', { defaultValue: '控制台排版字体' })}
            </CardTitle>
            <CardDescription>
              {t('appearance.fontDesc', {
                defaultValue:
                  '选择控制台显示采用的字体族，右侧可即时预览字形质感与数字等宽效果。',
              })}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className='grid grid-cols-1 gap-6 lg:grid-cols-2'>
              {/* Font Selector */}
              <FormField
                control={form.control}
                name='font'
                render={({ field }) => (
                  <FormItem className='space-y-3'>
                    <FormLabel>
                      {t('appearance.fontSelectLabel', {
                        defaultValue: '界面字体选择',
                      })}
                    </FormLabel>
                    <div className='relative max-w-sm'>
                      <FormControl>
                        <select
                          className={cn(
                            buttonVariants({ variant: 'outline' }),
                            'w-full appearance-none justify-between pe-8 font-normal capitalize',
                            'dark:bg-background dark:hover:bg-background'
                          )}
                          {...field}
                        >
                          {fonts.map((f) => (
                            <option key={f} value={f}>
                              {f}
                            </option>
                          ))}
                        </select>
                      </FormControl>
                      <ChevronDown className='pointer-events-none absolute inset-e-3 top-3 h-4 w-4 opacity-50' />
                    </div>
                    <FormDescription>
                      {t('appearance.fontNote', {
                        defaultValue:
                          '建议选用 Manrope 或 Inter 以获得最佳的数字辨识度与现代工程仪表质感。',
                      })}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {/* Typographic Live Preview */}
              <div className='space-y-2.5 rounded-lg border border-border/80 bg-muted/30 p-4'>
                <div className='flex items-center gap-2 text-xs font-semibold tracking-wider text-muted-foreground uppercase'>
                  <Type className='h-3.5 w-3.5 text-primary' />
                  <span>
                    {t('appearance.previewTitle', {
                      defaultValue: '即时排版效果预览',
                    })}
                    {' · '}
                    <span className='font-mono text-primary capitalize'>
                      {watchedFont}
                    </span>
                  </span>
                </div>
                <div
                  className={cn(
                    'space-y-1.5 rounded-md border border-border/60 bg-card p-3 shadow-2xs transition-all',
                    `font-${watchedFont}`
                  )}
                >
                  <p className='text-base font-bold tracking-tight'>
                    Aa Bb Gg Hh Kk 0123456789
                  </p>
                  <p className='text-xs font-medium text-foreground/90'>
                    边缘运维宿主 · Zhulong Edge System Monitor
                  </p>
                  <div className='flex flex-wrap gap-2 pt-1 font-mono text-[11px] text-muted-foreground'>
                    <span className='rounded bg-muted px-1.5 py-0.5'>
                      25.4 FPS
                    </span>
                    <span className='rounded bg-muted px-1.5 py-0.5'>
                      1080P60
                    </span>
                    <span className='rounded bg-muted px-1.5 py-0.5'>
                      4.2 MB/s
                    </span>
                    <span className='rounded bg-muted px-1.5 py-0.5'>
                      CPU 14%
                    </span>
                  </div>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Submit Button */}
        <div>
          <Button type='submit'>
            {t('appearance.submit', { defaultValue: '保存外观偏好' })}
          </Button>
        </div>
      </form>
    </Form>
  )
}
