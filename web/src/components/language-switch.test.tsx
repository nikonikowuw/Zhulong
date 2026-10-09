import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import i18n from '@/lib/i18n'
import { LanguageSwitch } from './language-switch'

describe('LanguageSwitch', () => {
  it('renders language switch button and switches language', async () => {
    // Reset to zh-Hans
    await i18n.changeLanguage('zh-Hans')

    const { getByRole, getByText } = await render(<LanguageSwitch />)

    const trigger = getByRole('button', { name: /switch language/i })
    await expect.element(trigger).toBeInTheDocument()

    // Click to open dropdown
    await userEvent.click(trigger)

    const englishOption = getByText('English')
    await expect.element(englishOption).toBeInTheDocument()

    // Click English
    await userEvent.click(englishOption)
    expect(i18n.language).toBe('en')

    // Click trigger again to switch back
    await userEvent.click(trigger)
    const zhHansOption = getByText('简体中文')
    await userEvent.click(zhHansOption)
    expect(i18n.language).toBe('zh-Hans')
  })
})
