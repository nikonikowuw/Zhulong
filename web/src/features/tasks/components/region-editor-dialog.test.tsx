import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { RegionEditorDialog } from './region-editor-dialog'

/**
 * 用可控的替身替换实时画面：真实 CameraFrameSurface 会尝试建立视频连接，
 * 无法在单测中确定性地驱动 'live' / 'unavailable' 两种状态。它自身已有独立测试。
 */
let reportedState: 'no-camera' | 'loading' | 'live' | 'unavailable' = 'live'

vi.mock('@/features/live', async (importOriginal) => {
  const React = await import('react')
  const actual = await importOriginal<typeof import('@/features/live')>()
  return {
    ...actual,
    CameraFrameSurface: ({
      paused,
      overlay,
      onStateChange,
    }: {
      paused?: boolean
      overlay?: (rect: {
        width: number
        height: number
        naturalWidth: number
        naturalHeight: number
        offsetX: number
        offsetY: number
      }) => React.ReactNode
      onStateChange?: (
        state: 'no-camera' | 'loading' | 'live' | 'unavailable'
      ) => void
    }) => {
      React.useEffect(() => {
        onStateChange?.(reportedState)
      }, [onStateChange])
      return React.createElement(
        'div',
        {
          'data-testid': 'frame-surface',
          'data-paused': String(Boolean(paused)),
        },
        overlay?.({
          width: 400,
          height: 200,
          naturalWidth: 400,
          naturalHeight: 200,
          offsetX: 0,
          offsetY: 0,
        })
      )
    },
  }
})

const ONE_ROI_JSON = '{"roi":[[0.1,0.1,0.5,0.5]],"mask":[]}'

async function renderDialog(value = ONE_ROI_JSON) {
  const onSave = vi.fn()
  const onOpenChange = vi.fn()
  const screen = await render(
    <RegionEditorDialog
      open
      onOpenChange={onOpenChange}
      cameraId='cam_1'
      cameraName='前门'
      algorithmName='helmet'
      value={value}
      onSave={onSave}
    />
  )
  return { screen, onSave, onOpenChange }
}

describe('RegionEditorDialog', () => {
  beforeEach(() => {
    reportedState = 'live'
    vi.restoreAllMocks()
  })

  it('saves the serialized region config once the stream is live', async () => {
    const { screen, onSave } = await renderDialog()

    await screen.getByRole('button', { name: '保存区域策略' }).click()

    expect(onSave).toHaveBeenCalledTimes(1)
    expect(JSON.parse(onSave.mock.calls[0][0])).toEqual({
      roi: [[0.1, 0.1, 0.5, 0.5]],
      mask: [],
    })
  })

  it('blocks saving and warns when the stream is not ready (AC8)', async () => {
    reportedState = 'unavailable'
    const { screen, onSave } = await renderDialog()

    await expect
      .element(screen.getByText('画面未就绪（只读浏览）'))
      .toBeInTheDocument()

    const saveButton = screen.getByRole('button', { name: '保存区域策略' })
    await expect.element(saveButton).toBeDisabled()

    // 即便绕过禁用态把点击强推进去，handler 也必须拒绝落库
    saveButton
      .element()
      .dispatchEvent(
        new MouseEvent('click', { bubbles: true, cancelable: true })
      )

    expect(onSave).not.toHaveBeenCalled()
  })

  it('restores a deleted rect via undo', async () => {
    const { screen } = await renderDialog()

    // 清单项与画布标签都含 "ROI #1"，因此用「删除」按钮作为清单条目的唯一标识
    await vi.waitFor(() => {
      expect(screen.getByTitle('删除区域').elements()).toHaveLength(1)
    })

    await screen.getByTitle('删除区域').click()
    await expect
      .element(screen.getByText('未设置关注区域（全画面推理上报）'))
      .toBeInTheDocument()
    await vi.waitFor(() => {
      expect(screen.getByTitle('删除区域').elements()).toHaveLength(0)
    })

    await screen.getByRole('button', { name: '撤销' }).click()
    await expect
      .element(screen.getByText('未设置关注区域（全画面推理上报）'))
      .not.toBeInTheDocument()
    await vi.waitFor(() => {
      expect(screen.getByTitle('删除区域').elements()).toHaveLength(1)
    })
  })

  it('freezes the live frame on demand so regions can be drawn precisely', async () => {
    const { screen } = await renderDialog()

    await expect
      .element(screen.getByTestId('frame-surface'))
      .toHaveAttribute('data-paused', 'false')

    await screen.getByRole('button', { name: '冻结画面' }).click()
    await expect
      .element(screen.getByTestId('frame-surface'))
      .toHaveAttribute('data-paused', 'true')

    await screen.getByRole('button', { name: '恢复播放' }).click()
    await expect
      .element(screen.getByTestId('frame-surface'))
      .toHaveAttribute('data-paused', 'false')
  })

  it('disables the ROI tool once the per-category limit is reached', async () => {
    const eightRois = JSON.stringify({
      roi: Array.from({ length: 8 }, (_, i) => [
        0.01 + i * 0.1,
        0.01,
        0.06 + i * 0.1,
        0.06,
      ]),
      mask: [],
    })
    const { screen } = await renderDialog(eightRois)

    await expect
      .element(screen.getByRole('button', { name: /ROI 关注区/ }))
      .toBeDisabled()
  })
})
