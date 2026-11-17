import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { ChannelTaskClipSection } from './channel-task-clip-section'

function Harness({
  initial,
  showRecognitionFilter,
  onChange,
}: {
  initial: string
  showRecognitionFilter: boolean
  onChange: (json: string) => void
}) {
  const [json, setJson] = useState(initial)
  return (
    <ChannelTaskClipSection
      algoId='algo-1'
      clipConfigJson={json}
      showRecognitionFilter={showRecognitionFilter}
      onChange={(next) => {
        setJson(next)
        onChange(next)
      }}
    />
  )
}

describe('ChannelTaskClipSection', () => {
  it('starts disabled for an empty config and serializes defaults on enable', async () => {
    const onChange = vi.fn()
    const screen = await render(
      <Harness initial='{}' showRecognitionFilter={false} onChange={onChange} />
    )

    const toggle = screen.getByRole('switch', { name: '事件录像' })
    await expect.element(toggle).not.toBeChecked()
    expect(screen.container.querySelector('#clip-pre-algo-1')).toBeNull()

    await userEvent.click(toggle)

    expect(JSON.parse(onChange.mock.lastCall?.[0] as string)).toEqual({
      enabled: true,
      preSeconds: 5,
      postSeconds: 10,
      recognitionFilter: 'all',
    })
    await expect.element(screen.getByLabelText('前段秒数')).toHaveValue('5')
    await expect.element(screen.getByLabelText('后段秒数')).toHaveValue('10')
    // 检测类算法不展示识别过滤
    expect(screen.container.querySelector('#clip-filter-algo-1')).toBeNull()
  })

  it('clamps seconds input to the allowed range', async () => {
    const onChange = vi.fn()
    const screen = await render(
      <Harness
        initial='{"enabled":true,"preSeconds":5,"postSeconds":10,"recognitionFilter":"all"}'
        showRecognitionFilter={false}
        onChange={onChange}
      />
    )

    const pre = screen.getByLabelText('前段秒数')
    await userEvent.clear(pre)
    await userEvent.type(pre, '40{Enter}')

    expect(JSON.parse(onChange.mock.lastCall?.[0] as string)).toMatchObject({
      preSeconds: 15,
    })

    const post = screen.getByLabelText('后段秒数')
    await userEvent.clear(post)
    await userEvent.type(post, '0{Enter}')
    expect(JSON.parse(onChange.mock.lastCall?.[0] as string)).toMatchObject({
      preSeconds: 15,
      postSeconds: 1,
    })
  })

  it('shows the recognition filter for recognition algorithms', async () => {
    const onChange = vi.fn()
    const screen = await render(
      <Harness
        initial='{"enabled":true,"preSeconds":5,"postSeconds":10,"recognitionFilter":"all"}'
        showRecognitionFilter
        onChange={onChange}
      />
    )

    const trigger = screen.getByRole('combobox', { name: '识别过滤' })
    await expect.element(trigger).toHaveTextContent('全部识别')
    await userEvent.click(trigger)
    await userEvent.click(screen.getByRole('option', { name: '仅陌生人' }))

    expect(JSON.parse(onChange.mock.lastCall?.[0] as string)).toMatchObject({
      enabled: true,
      recognitionFilter: 'stranger',
    })
  })
})
