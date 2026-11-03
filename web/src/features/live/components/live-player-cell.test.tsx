import { beforeEach, describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type LiveCameraItem, type LiveCellState } from '../types'
import { LivePlayerCell } from './live-player-cell'

/** 通过 vi.hoisted 暴露替身控制点，避免 mock 工厂访问尚未初始化的顶层绑定。 */
const mocks = vi.hoisted(() => ({
  /** 传输层状态：本文件只关心「连上之后 UI 该显示什么」。 */
  status: 'connected' as string,
  /** 是否支持 WebCodecs：false 代表局域网 HTTP（非安全上下文）。 */
  supportsWebCodecs: false,
  /** 能力帧（`stream_info`）；null 模拟旧后端不发能力帧。 */
  streamInfo: null as unknown,
}))

vi.mock('../hooks/use-stream-connection', () => ({
  useStreamConnection: () => ({
    status: mocks.status,
    errorMessage: null,
    stats: {},
    retry: vi.fn(),
    streamInfo: mocks.streamInfo,
  }),
}))

vi.mock('../services/webcodecs-decoder', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('../services/webcodecs-decoder')>()
  return {
    ...actual,
    isWebCodecsSupported: () => mocks.supportsWebCodecs,
    getWebCodecsSupportIssue: () =>
      mocks.supportsWebCodecs ? null : 'insecure_context',
  }
})

describe('LivePlayerCell 交互测试', () => {
  beforeEach(() => {
    mocks.status = 'connected'
    mocks.supportsWebCodecs = false
    mocks.streamInfo = null
  })

  const dummyCamera: LiveCameraItem = {
    id: 'cam-01',
    name: '东大门测试枪机',
    host: '192.168.1.101',
    status: 'online',
    hasSubStream: true,
    codec: 'h265',
  }

  const emptyCell: LiveCellState = {
    cellId: 2,
    cameraId: null,
    streamType: 'main',
    isMuted: true,
  }

  it('支持拖拽摄像机并成功触发 onDropCamera 回调', async () => {
    const onDropCamera = vi.fn()
    const { container } = await render(
      <LivePlayerCell
        cell={emptyCell}
        isActive={false}
        isMaximized={false}
        onSelect={vi.fn()}
        onClear={vi.fn()}
        onToggleMaximize={vi.fn()}
        onSetMute={vi.fn()}
        onSetStreamType={vi.fn()}
        onDropCamera={onDropCamera}
      />
    )

    const cellEl = container.firstElementChild as HTMLElement
    expect(cellEl).not.toBeNull()

    // 模拟 drop 事件并携带序列化的摄像机对象
    const dropEvent = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(dropEvent, 'dataTransfer', {
      value: {
        getData: (format: string) =>
          format === 'application/json' ? JSON.stringify(dummyCamera) : '',
      },
    })

    cellEl.dispatchEvent(dropEvent)
    expect(onDropCamera).toHaveBeenCalledWith(dummyCamera)
  })

  it('点击空闲窗口应触发 onSelect 回调', async () => {
    const onSelect = vi.fn()
    const { container } = await render(
      <LivePlayerCell
        cell={emptyCell}
        isActive={false}
        isMaximized={false}
        onSelect={onSelect}
        onClear={vi.fn()}
        onToggleMaximize={vi.fn()}
        onSetMute={vi.fn()}
        onSetStreamType={vi.fn()}
      />
    )

    const cellEl = container.firstElementChild as HTMLElement
    expect(cellEl).not.toBeNull()
    cellEl.click()

    expect(onSelect).toHaveBeenCalled()
  })

  /**
   * 回归保护：非安全上下文下「等待首帧」遮罩绝不能盖住「切换至 HTTPS」引导。
   *
   * 这条路径上解码器根本不存在，首帧永远不会来；只看 status 的遮罩会永久旋转，
   * 用户既看不到原因也点不到出路。
   */
  it('非安全上下文下不展示等待首帧遮罩，而是露出切换 HTTPS 的引导', async () => {
    const cell: LiveCellState = {
      cellId: 1,
      cameraId: 'cam-01',
      cameraName: '东大门测试枪机',
      streamType: 'main',
      isMuted: true,
    }

    const { container } = await render(
      <LivePlayerCell
        cell={cell}
        isActive={false}
        isMaximized={false}
        onSelect={vi.fn()}
        onClear={vi.fn()}
        onToggleMaximize={vi.fn()}
        onSetMute={vi.fn()}
        onSetStreamType={vi.fn()}
      />
    )

    const text = container.textContent ?? ''
    expect(text).not.toContain('等待首帧渲染')
    expect(text).toContain('WebCodecs 需要安全上下文')
    expect(text).toContain('切换至 HTTPS')
  })
})

/**
 * 喇叭的能力门控。
 *
 * 两种置灰的语义完全不同，用户能做的事也不同：
 *   - 该路无音频（`present=false` / 旧后端不发能力帧）→ 找设备管理员；
 *   - 浏览器不支持该音频格式 → 换浏览器 / 上 HTTPS。
 * 因此按钮必须 disabled，且 title/aria-label 要能说明原因。
 */
describe('LivePlayerCell 音频能力', () => {
  const cameraCell: LiveCellState = {
    cellId: 0,
    cameraId: 'cam-audio',
    cameraName: '带麦克风的枪机',
    hasSubStream: true,
    streamType: 'main',
    isMuted: true,
  }

  function makeStreamInfo(present: boolean, codec = 'pcma') {
    return {
      type: 'stream_info' as const,
      cameraId: 'cam-audio',
      role: 'main',
      v: 1,
      audio: { present, codec, sampleRate: 8000, channels: 1, config: '' },
    }
  }

  function renderCell(onSetMute = vi.fn()) {
    return render(
      <LivePlayerCell
        cell={cameraCell}
        isActive={false}
        isMaximized={false}
        onSelect={vi.fn()}
        onClear={vi.fn()}
        onToggleMaximize={vi.fn()}
        onSetMute={onSetMute}
        onSetStreamType={vi.fn()}
      />
    )
  }

  /** 找到喇叭按钮：底部控制岛里 title/aria-label 命中静音相关文案的那个。 */
  function findMuteButton(container: HTMLElement): HTMLButtonElement | null {
    const buttons = Array.from(container.querySelectorAll('button'))
    return (
      (buttons.find((button) => {
        const label = `${button.getAttribute('title') ?? ''} ${button.getAttribute('aria-label') ?? ''}`
        return (
          label.includes('静音') ||
          label.includes('开启声音') ||
          label.includes('该路无音频') ||
          label.includes('正在确认该路音频能力') ||
          label.includes('浏览器不支持该音频格式')
        )
      }) as HTMLButtonElement | undefined) ?? null
    )
  }

  it('present=false：喇叭 disabled 且提示「该路无音频」', async () => {
    mocks.streamInfo = makeStreamInfo(false)
    const { container } = await renderCell()

    const button = findMuteButton(container)
    expect(button).not.toBeNull()
    expect(button?.disabled).toBe(true)
    expect(button?.getAttribute('title')).toBe('该路无音频')
  })

  it('streamInfo=null（能力帧还没到 / 旧后端）：置灰但不报「无音频」', async () => {
    // 「还不知道」与「确定没有」必须分开：能力帧在建连后几帧才到，
    // 归成 noAudio 会让正常相机在首帧期间弹出一条设备故障。
    mocks.streamInfo = null
    const { container } = await renderCell()

    const button = findMuteButton(container)
    expect(button?.disabled).toBe(true)
    expect(button?.getAttribute('title')).toBe('正在确认该路音频能力')
  })

  it('present=true：喇叭可用，title 为「开启声音」', async () => {
    mocks.streamInfo = makeStreamInfo(true)
    const { container } = await renderCell()

    const button = findMuteButton(container)
    expect(button?.disabled).toBe(false)
    expect(button?.getAttribute('title')).toBe('开启声音')
  })

  it('disabled 的喇叭点击不产生焦点（onSetMute 不被调用）', async () => {
    mocks.streamInfo = makeStreamInfo(false)
    const onSetMute = vi.fn()
    const { container } = await renderCell(onSetMute)

    const button = findMuteButton(container)
    button?.click()
    expect(onSetMute).not.toHaveBeenCalled()
  })
})
