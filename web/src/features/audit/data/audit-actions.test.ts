import { describe, expect, it } from 'vitest'
import en from '../locales/en.json'
import zhHans from '../locales/zh-Hans.json'
import zhHant from '../locales/zh-Hant.json'
import { AUDIT_ACTION_KEYS } from './audit-actions'

const locales: { name: string; actionNames: Record<string, string> }[] = [
  { name: 'zh-Hans', actionNames: zhHans.actionNames },
  { name: 'zh-Hant', actionNames: zhHant.actionNames },
  { name: 'en', actionNames: en.actionNames },
]

const declaredKeys: string[] = [...AUDIT_ACTION_KEYS]
const declaredKeySet = new Set(declaredKeys)

describe('audit action catalog', () => {
  it('declares no duplicate action keys', () => {
    expect(declaredKeySet.size).toBe(declaredKeys.length)
  })

  it.each(locales)(
    'labels every declared action in $name',
    ({ actionNames }) => {
      const missing = declaredKeys.filter((key) => !actionNames[key])

      expect(missing).toEqual([])
    }
  )

  it.each(locales)(
    'carries no action label missing from the catalog in $name',
    ({ actionNames }) => {
      const stale = Object.keys(actionNames).filter(
        (key) => !declaredKeySet.has(key)
      )

      expect(stale).toEqual([])
    }
  )

  it.each(locales)(
    'never ships an empty action label in $name',
    ({ actionNames }) => {
      const empty = declaredKeys.filter(
        (key) => !(actionNames[key] ?? '').trim()
      )

      expect(empty).toEqual([])
    }
  )

  it('keeps all locales aligned on the action key set', () => {
    const reference = Object.keys(zhHans.actionNames).sort()

    for (const { name, actionNames } of locales) {
      expect({ name, keys: Object.keys(actionNames).sort() }).toEqual({
        name,
        keys: reference,
      })
    }
  })
})
