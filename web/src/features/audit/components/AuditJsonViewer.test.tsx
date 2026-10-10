import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import { AuditJsonViewer } from './AuditJsonViewer'

describe('AuditJsonViewer', () => {
  it('highlights JSON keys and values without changing content', async () => {
    const json = '{\n  "camera": "gate",\n  "enabled": true,\n  "fps": 25\n}'
    const { getByText } = await render(
      <AuditJsonViewer value={json} highlighted />
    )

    await expect.element(getByText('"camera"')).toHaveClass(/text-sky/)
    await expect.element(getByText('"gate"')).toHaveClass(/text-emerald/)
    await expect.element(getByText('true')).toHaveClass(/text-violet/)
  })

  it('renders non-JSON detail as plain text', async () => {
    const { getByText } = await render(
      <AuditJsonViewer value='raw detail' highlighted={false} />
    )

    await expect.element(getByText('raw detail')).toBeInTheDocument()
  })
})
