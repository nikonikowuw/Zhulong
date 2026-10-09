import { Languages, Check } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { type SupportedLanguage } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

const LANGUAGES: Array<{ code: SupportedLanguage; label: string }> = [
  { code: 'zh-Hans', label: '简体中文' },
  { code: 'zh-Hant', label: '繁體中文' },
  { code: 'en', label: 'English' },
]

export function LanguageSwitch() {
  const { i18n } = useTranslation()
  const currentLang = (i18n.resolvedLanguage ||
    i18n.language ||
    'zh-Hans') as SupportedLanguage

  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          variant='ghost'
          size='icon'
          className='scale-95 rounded-full'
          aria-label='Switch Language'
        >
          <Languages className='size-[1.2rem]' />
          <span className='sr-only'>Switch Language</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        {LANGUAGES.map((item) => (
          <DropdownMenuItem
            key={item.code}
            onClick={() => void i18n.changeLanguage(item.code)}
          >
            {item.label}
            <Check
              size={14}
              className={cn('ms-auto', currentLang !== item.code && 'hidden')}
            />
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
