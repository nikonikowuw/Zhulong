import { type TFunction } from 'i18next'

/**
 * zod 校验文案的统一出口。
 *
 * zod 的 message 会原样进入 `FormMessage`，因此必须在这里就经 `t` 解析
 * （`validation.*` 命名空间在 `locales/`）；没有 i18n 时回退成 key 本身。
 */
export function createMessageHelper(t?: TFunction) {
  return (key: string, options?: Record<string, unknown>): string =>
    t ? t(`validation.${key}`, { ns: 'gb28181', ...options }) : key
}
