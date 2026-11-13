import { describe, expect, it } from 'vitest'
import i18n, { SUPPORTED_LANGUAGES, resources } from '@/lib/i18n'
import { taskFormSchema } from '@/features/tasks/data/schema'

function flattenKeys(obj: Record<string, unknown>, prefix = ''): Set<string> {
  const keys = new Set<string>()
  for (const [k, v] of Object.entries(obj)) {
    const nextKey = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      for (const nested of flattenKeys(v as Record<string, unknown>, nextKey)) {
        keys.add(nested)
      }
    } else {
      keys.add(nextKey)
    }
  }
  return keys
}

describe('i18n configuration & parity', () => {
  it('keeps identical translation keys across all supported languages and namespaces', () => {
    const namespaces = Object.keys(resources['zh-Hans']) as Array<
      keyof (typeof resources)['zh-Hans']
    >

    for (const ns of namespaces) {
      const baseKeys = flattenKeys(
        resources['zh-Hans'][ns] as unknown as Record<string, unknown>
      )
      for (const lang of SUPPORTED_LANGUAGES) {
        const langKeys = flattenKeys(
          resources[lang][ns] as unknown as Record<string, unknown>
        )
        expect(Array.from(langKeys).sort()).toEqual(Array.from(baseKeys).sort())
      }
    }
  })

  it('syncs <html lang> and translates nav/error keys across all 3 languages', async () => {
    await i18n.changeLanguage('zh-Hans')
    expect(document.documentElement.lang).toBe('zh-Hans')
    expect(i18n.t('nav.E-Map')).toBe('电子地图')
    expect(i18n.t('nav.Storage')).toBe('存储与生命周期')
    expect(i18n.t('errors.404.title')).toBe('页面未找到')

    await i18n.changeLanguage('zh-Hant')
    expect(document.documentElement.lang).toBe('zh-Hant')
    expect(i18n.t('nav.E-Map')).toBe('電子地圖')
    expect(i18n.t('nav.Storage')).toBe('儲存與生命週期')
    expect(i18n.t('errors.404.title')).toBe('找不到頁面')

    await i18n.changeLanguage('en')
    expect(document.documentElement.lang).toBe('en')
    expect(i18n.t('nav.E-Map')).toBe('Spatial E-Map')
    expect(i18n.t('nav.Storage')).toBe('Storage & Lifecycle')
    expect(i18n.t('errors.404.title')).toBe('Oops! Page Not Found!')
  })

  it('evaluates taskFormSchema validation messages dynamically per language', async () => {
    await i18n.changeLanguage('zh-Hans')
    const zhRes = taskFormSchema.safeParse({ cameraId: '', algorithmId: '' })
    expect(zhRes.success).toBe(false)
    if (!zhRes.success) {
      expect(zhRes.error.issues[0].message).toBe('请选择关联摄像头')
    }

    await i18n.changeLanguage('en')
    const enRes = taskFormSchema.safeParse({ cameraId: '', algorithmId: '' })
    expect(enRes.success).toBe(false)
    if (!enRes.success) {
      expect(enRes.error.issues[0].message).toBe('Please select a camera')
    }
  })
})
