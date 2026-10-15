import { describe, it, expect } from 'vitest'
import { render } from 'vitest-browser-react'
import { AlgorithmStatusBadge } from './algorithm-status-badge'

describe('AlgorithmStatusBadge', () => {
  it('renders ACTIVE badge correctly', async () => {
    const { getByText } = await render(<AlgorithmStatusBadge status='ACTIVE' />)
    await expect.element(getByText('已激活')).toBeInTheDocument()
  })

  it('renders DISABLED badge correctly', async () => {
    const { getByText } = await render(<AlgorithmStatusBadge status='DISABLED' />)
    await expect.element(getByText('已停用')).toBeInTheDocument()
  })

  it('renders RUNNING badge correctly', async () => {
    const { getByText } = await render(<AlgorithmStatusBadge status='RUNNING' />)
    await expect.element(getByText('运行中')).toBeInTheDocument()
  })

  it('renders CRASHED badge correctly', async () => {
    const { getByText } = await render(<AlgorithmStatusBadge status='CRASHED' />)
    await expect.element(getByText('崩溃')).toBeInTheDocument()
  })
})
