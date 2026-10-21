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
    await expect.element(englishOption).not.toBeInTheDocument()

    const updatedTrigger = getByRole('button', { name: /switch language/i })
    await expect
      .element(updatedTrigger)
      .toHaveAttribute('aria-expanded', 'false')

    // Re-open only after the previous menu has closed.
    await userEvent.click(updatedTrigger)
    await expect
      .element(updatedTrigger)
      .toHaveAttribute('aria-expanded', 'true')

    const zhHansOption = getByText('简体中文')
    await expect.element(zhHansOption).toBeInTheDocument()
    await userEvent.click(zhHansOption)
    expect(i18n.language).toBe('zh-Hans')
  })
})
