import { describe, it, expect } from 'vitest'
import { render } from 'vitest-browser-react'
import { TaskStatusBadge } from './task-status-badge'

describe('TaskStatusBadge', () => {
  it('renders running status when enabled is true', async () => {
    const { getByText } = await render(<TaskStatusBadge enabled={true} />)
    expect(getByText('运行中')).toBeDefined()
  })

  it('renders paused status when enabled is false', async () => {
    const { getByText } = await render(<TaskStatusBadge enabled={false} />)
    expect(getByText('已暂停')).toBeDefined()
  })
})
