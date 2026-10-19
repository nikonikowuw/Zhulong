import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { type ImageRect } from '../services/frame-geometry'
import { type ParsedPacket } from '../types'
import { CameraFrameSurface } from './camera-frame-surface'

/** 通过 vi.hoisted 暴露替身控制点，避免 mock 工厂访问尚未初始化的顶层绑定。 */
const mocks = vi.hoisted(() => ({
  decode: vi.fn(),
  close: vi.fn(),
  packetSink: undefined as undefined | ((packet: ParsedPacket) => void),
  decoderOnFrame: undefined as undefined | ((frame: VideoFrame) => void),
  status: 'idle' as string,
  supported: true,
}))

vi.mock('../hooks/use-stream-connection', () => ({
  useStreamConnection: (options: {
    onPacket?: (packet: ParsedPacket) => void
  }) => {
    mocks.packetSink = options.onPacket
    return {
      status: mocks.status,
      errorMessage: undefined,
      stats: {
        fps: 0,
        resolution: '0x0',
        bitrateKbps: 0,
        decoderMode: 'WebCodecs',
      },
      retry: vi.fn(),
    }
  },
}))

vi.mock('../services/webcodecs-decoder', () => ({
  isWebCodecsSupported: () => mocks.supported,
  StreamDecoder: class {
    constructor(options: { onFrame: (frame: VideoFrame) => void }) {
      mocks.decoderOnFrame = options.onFrame
    }
    decode(packet: ParsedPacket) {
      mocks.decode(packet)
    }
    close() {
      mocks.close()
    }
    reset() {}
  },
}))

const SURFACE_WIDTH = 400
const SURFACE_HEIGHT = 200

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  })
}

function makeFrame(width: number, height: number): VideoFrame {
  return new VideoFrame(new Uint8ClampedArray(width * height * 4), {
    format: 'RGBA',
    codedWidth: width,
    codedHeight: height,
    timestamp: 0,
  })
}

async function renderSurface(overlay?: (rect: ImageRect) => React.ReactNode) {
  const onStateChange = vi.fn()
  const screen = await render(
    <div style={{ width: `${SURFACE_WIDTH}px`, height: `${SURFACE_HEIGHT}px` }}>
      <CameraFrameSurface
        cameraId='cam_1'
        onStateChange={onStateChange}
        overlay={overlay}
      />
    </div>
  )
  return { screen, onStateChange }
}

describe('CameraFrameSurface', () => {
  beforeEach(() => {
    mocks.status = 'idle'
    mocks.supported = true
    mocks.decode.mockClear()
    mocks.close.mockClear()
    mocks.packetSink = undefined
    mocks.decoderOnFrame = undefined
  })

  it('renders no-camera state when cameraId is null', async () => {
    const handleState = vi.fn()
    const screen = await render(
      <CameraFrameSurface cameraId={null} onStateChange={handleState} />
    )
    expect(screen.getByText('未指定摄像机')).not.toBeNull()
    expect(handleState).toHaveBeenCalledWith('no-camera')
  })

  it('renders loading state initially when cameraId is provided', async () => {
    const handleState = vi.fn()
    const screen = await render(
      <CameraFrameSurface cameraId='cam_1' onStateChange={handleState} />
    )
    expect(screen.getByText('连接视频流...')).not.toBeNull()
    expect(handleState).toHaveBeenCalledWith('loading')
  })

  it('renders the unavailable state with a retry action when the stream errors', async () => {
    mocks.status = 'error'
    const { screen, onStateChange } = await renderSurface()

    await expect.element(screen.getByText('视频流无法连接')).toBeInTheDocument()
    await expect
      .element(screen.getByRole('button', { name: '重试连接' }))
      .toBeInTheDocument()
    expect(onStateChange).toHaveBeenCalledWith('unavailable')
  })

  it('reports unavailable without a retry action when WebCodecs is unsupported', async () => {
    mocks.supported = false
    const { screen, onStateChange } = await renderSurface()

    await expect
      .element(screen.getByText('当前环境不支持硬件加速解码 (WebCodecs)'))
      .toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试连接' }).query()).toBeNull()
    expect(onStateChange).toHaveBeenCalledWith('unavailable')
  })

  it('drops incoming packets while paused so the frame stays frozen', async () => {
    mocks.status = 'connected'
    const screen = await render(
      <div
        style={{ width: `${SURFACE_WIDTH}px`, height: `${SURFACE_HEIGHT}px` }}
      >
        <CameraFrameSurface cameraId='cam_1' paused />
      </div>
    )
    expect(screen).toBeTruthy()

    mocks.packetSink?.({} as ParsedPacket)

    expect(mocks.decode).not.toHaveBeenCalled()
  })

  it('mounts the overlay once a frame has been drawn and reports live', async () => {
    mocks.status = 'connected'
    const seen: ImageRect[] = []
    const { screen, onStateChange } = await renderSurface((rect) => {
      seen.push(rect)
      return <div data-testid='overlay-content' />
    })

    // 首帧渲染前不挂叠加层，避免用错误的坐标空间作画
    expect(screen.getByTestId('overlay-content').query()).toBeNull()

    expect(mocks.decoderOnFrame).toBeDefined()
    const frame = makeFrame(800, 200)
    mocks.decoderOnFrame?.(frame)
    await nextFrame()

    await expect
      .element(screen.getByTestId('overlay-content'))
      .toBeInTheDocument()
    // 状态经 useEffect 在提交后 flush，与叠加层挂载不在同一拍：
    // 叠加层已就位不代表 'live' 回调已触发，必须重试等待而非同步断言。
    await vi.waitFor(() => {
      expect(onStateChange).toHaveBeenCalledWith('live')
    })
    // 叠加层拿到的是画面固有尺寸，元素盒尺寸由 frame-geometry 单测覆盖
    expect(seen[seen.length - 1].naturalWidth).toBe(800)
    expect(seen[seen.length - 1].naturalHeight).toBe(200)
  })
})
