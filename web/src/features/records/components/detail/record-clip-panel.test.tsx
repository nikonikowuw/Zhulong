import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { recordsApi } from '../../api/records-api'
import { type Alarm, type ClipInfo } from '../../data/schema'
import clipFixtureUrl from './__fixtures__/clip-h264.mp4?url'
import { RecordClipPanel } from './record-clip-panel'

// 3 秒、10fps、每秒一个关键帧的 H.264 MP4（ffmpeg testsrc 生成）。
// 转成绝对 URL：getMediaUrl 对 http(s) 原样放行，不会拼上 /api/v1/media/content/。
const FIXTURE_URL = new URL(clipFixtureUrl, window.location.href).href

function makeClip(overrides: Partial<ClipInfo> = {}): ClipInfo {
  return {
    id: 'clip-1',
    status: 'completed',
    partial: false,
    durationMs: 3000,
    offsetMs: 1500,
    mediaPath: FIXTURE_URL,
    codec: 'h264',
    ...overrides,
  }
}

function createWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

describe('RecordClipPanel', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('plays a supported clip and seeks to the event offset', async () => {
    const probe = vi.fn(() => true)
    const screen = await render(
      <RecordClipPanel
        kind='alarm'
        recordId='alarm-1'
        clip={makeClip()}
        probe={probe}
      />,
      { wrapper: createWrapper() }
    )

    const video = screen.getByLabelText('事件录像播放器')
    await expect.element(video).toBeInTheDocument()
    expect(probe).toHaveBeenCalledWith('h264')

    const element = video.element() as HTMLVideoElement
    expect(element.src).toBe(FIXTURE_URL)
    // 真实解码：等 loadedmetadata 触发、seek 落到 1.5s。
    await vi.waitFor(
      () => {
        expect(element.readyState).toBeGreaterThanOrEqual(
          HTMLMediaElement.HAVE_METADATA
        )
        expect(element.currentTime).toBeCloseTo(1.5, 1)
      },
      { timeout: 5000 }
    )

    const download = screen.getByRole('link', { name: '下载录像' })
    await expect.element(download).toHaveAttribute('href', FIXTURE_URL)
    await expect.element(download).toHaveAttribute('download', 'clip-h264.mp4')
  })

  it('clamps an offset past the end of a truncated clip and flags it', async () => {
    const screen = await render(
      <RecordClipPanel
        kind='alarm'
        recordId='alarm-1'
        clip={makeClip({ status: 'truncated', partial: true, offsetMs: 9000 })}
        probe={() => true}
      />,
      { wrapper: createWrapper() }
    )

    await expect.element(screen.getByText('不完整')).toBeInTheDocument()
    const element = screen
      .getByLabelText('事件录像播放器')
      .element() as HTMLVideoElement
    await vi.waitFor(
      () => {
        expect(element.readyState).toBeGreaterThanOrEqual(
          HTMLMediaElement.HAVE_METADATA
        )
        expect(element.currentTime).toBeCloseTo(element.duration, 1)
      },
      { timeout: 5000 }
    )
  })

  it('shows the download hint when the codec is unsupported', async () => {
    const screen = await render(
      <RecordClipPanel
        kind='recognition'
        recordId='rec-1'
        clip={makeClip({ codec: 'hevc', mediaPath: 'recordings/cam-1/a.mp4' })}
        probe={(codec) => codec !== 'hevc'}
      />,
      { wrapper: createWrapper() }
    )

    await expect
      .element(screen.getByText('当前浏览器不支持该编码，请下载播放'))
      .toBeInTheDocument()
    expect(screen.container.querySelector('video')).toBeNull()
    await expect
      .element(screen.getByRole('link', { name: '下载录像' }))
      .toHaveAttribute('href', '/api/v1/media/content/recordings/cam-1/a.mp4')
  })

  it('shows a playback error when decoding fails despite the probe', async () => {
    const screen = await render(
      <RecordClipPanel
        kind='alarm'
        recordId='alarm-1'
        clip={makeClip({ mediaPath: 'data:video/mp4;base64,AAAA' })}
        probe={() => true}
      />,
      { wrapper: createWrapper() }
    )

    await expect
      .element(screen.getByText('录像加载或播放失败。'))
      .toBeInTheDocument()
  })

  it('shows the recording state and refreshes the detail on demand', async () => {
    const completed: Alarm = {
      id: 'alarm-1',
      cameraId: 'cam-1',
      algorithmId: 'algo-1',
      alarmType: 'intrusion',
      score: 0.9,
      box: [0, 0, 1, 1],
      imagePath: '',
      cropPath: '',
      extra: {},
      pts: 0,
      clip: makeClip(),
      capturedAt: '2026-11-17T00:00:00Z',
      createdAt: '2026-11-17T00:00:00Z',
    }
    const getAlarm = vi
      .spyOn(recordsApi, 'getAlarm')
      .mockResolvedValue(completed)

    const screen = await render(
      <RecordClipPanel
        kind='alarm'
        recordId='alarm-1'
        clip={makeClip({ status: 'recording', durationMs: 0 })}
        probe={() => true}
      />,
      { wrapper: createWrapper() }
    )

    await expect.element(screen.getByText('录制中').first()).toBeInTheDocument()
    expect(screen.container.querySelector('video')).toBeNull()
    expect(getAlarm).not.toHaveBeenCalled()

    await userEvent.click(screen.getByRole('button', { name: '刷新' }))

    expect(getAlarm).toHaveBeenCalledWith('alarm-1')
    await expect
      .element(screen.getByLabelText('事件录像播放器'))
      .toBeInTheDocument()
  })

  it('shows the failure message without a download link', async () => {
    const screen = await render(
      <RecordClipPanel
        kind='alarm'
        recordId='alarm-1'
        clip={makeClip({ status: 'failed' })}
        probe={() => true}
      />,
      { wrapper: createWrapper() }
    )

    await expect
      .element(screen.getByText('录像失败').first())
      .toBeInTheDocument()
    expect(screen.container.querySelector('a[download]')).toBeNull()
    expect(screen.container.querySelector('video')).toBeNull()
  })
})
