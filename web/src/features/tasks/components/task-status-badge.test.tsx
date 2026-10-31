import { describe, it, expect } from 'vitest'
import { render } from 'vitest-browser-react'
import { TaskStatusBadge } from './task-status-badge'

describe('TaskStatusBadge', () => {
  it('renders running status when enabled is true', async () => {
    const { getByText } = await render(<TaskStatusBadge enabled={true} />)

    await expect.element(getByText('运行中')).toBeInTheDocument()
    expect(getByText('已暂停').query()).toBeNull()
  })

  it('renders paused status when enabled is false', async () => {
    const { getByText } = await render(<TaskStatusBadge enabled={false} />)

    await expect.element(getByText('已暂停')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders channel paused status when isChannelEnabled is false', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} isChannelEnabled={false} />
    )

    await expect.element(getByText('通道已暂停')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders standby status when enabled is true but algorithm is DISABLED', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} algorithmStatus='DISABLED' />
    )

    await expect.element(getByText('待命 (未激活)')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders error status when algorithm is CRASH_LOOP', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} algorithmStatus='CRASH_LOOP' />
    )

    await expect.element(getByText('异常')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders stream disconnected status when camera health is offline', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} cameraHealth='offline' />
    )

    await expect.element(getByText('视频流断开')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders stream disconnected status when camera health is unknown', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} cameraHealth='unknown' />
    )

    await expect.element(getByText('视频流断开')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders stream reconnecting status when camera session is reconnecting', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} cameraSession='reconnecting' />
    )

    await expect.element(getByText('视频流重连中')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders stream error status when camera health is error', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} cameraHealth='error' />
    )

    await expect.element(getByText('视频流异常')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders camera disabled status when isCameraEnabled is false', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} isCameraEnabled={false} />
    )

    await expect.element(getByText('摄像机已停用')).toBeInTheDocument()
    expect(getByText('运行中').query()).toBeNull()
  })

  it('renders running status when camera health is online', async () => {
    const { getByText } = await render(
      <TaskStatusBadge enabled={true} cameraHealth='online' />
    )

    await expect.element(getByText('运行中')).toBeInTheDocument()
    expect(getByText('视频流断开').query()).toBeNull()
  })
})
