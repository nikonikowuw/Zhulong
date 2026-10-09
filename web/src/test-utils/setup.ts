import { beforeEach } from 'vitest'
import i18n from '@/lib/i18n'

beforeEach(async () => {
  await i18n.changeLanguage('zh-Hans')
})
