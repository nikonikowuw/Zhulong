import { act } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { emptyDraft, type RegionDraft } from '../data/region-config'
import { RegionCanvas, type RegionCanvasProps } from './region-canvas'

const draft: RegionDraft = {
  roi: [{ id: 'roi-1', rect: [0.1, 0.1, 0.5, 0.5] }],
  mask: [{ id: 'mask-1', rect: [0.6, 0.6, 0.8, 0.8] }],
}

/** 画布尺寸固定，使归一化坐标换算可预测。 */
const SURFACE_WIDTH = 400
const SURFACE_HEIGHT = 200

/**
 * 测试环境不加载 Tailwind，`h-full w-full` 不会生效，画布根节点会塌成 0 高度，
 * 归一化换算随即拿到 null。这里补一条最小样式让根节点撑满固定尺寸的包裹层。
 */
function injectCanvasSizeCss(): void {
  if (document.getElementById('region-canvas-test-size')) return
  const style = document.createElement('style')
  style.id = 'region-canvas-test-size'
  style.textContent =
    '[data-testid="region-surface"] > * { width: 100%; height: 100%; }'
  document.head.appendChild(style)
}

function surfaceElement(): HTMLElement {
  const el = document.querySelector('[data-testid="region-surface"]')
  if (!el) throw new Error('region surface not rendered')
  return el as HTMLElement
}

/**
 * RegionCanvas 的根节点。事件必须派发到它身上——派发到外层包裹 div 只会向上冒泡，
 * 永远到不了画布自己的 onMouseDown。
 */
function canvasRoot(): HTMLElement {
  const el = surfaceElement().firstElementChild
  if (!el) throw new Error('region canvas root not rendered')
  return el as HTMLElement
}

/**
 * 模拟一次完整拖拽。
 *
 * mousedown 与后续 mousemove/mouseup 之间必须 `await act`：window 级监听器是在
 * mousedown 触发的 effect 里注册的，同步连发事件时监听器还没挂上，拖拽会被静默吞掉。
 */
async function dispatchDrag(
  from: { x: number; y: number },
  to: { x: number; y: number }
): Promise<void> {
  const rect = canvasRoot().getBoundingClientRect()
  const at = (p: { x: number; y: number }) => ({
    clientX: rect.left + rect.width * p.x,
    clientY: rect.top + rect.height * p.y,
  })

  await act(async () => {
    canvasRoot().dispatchEvent(
      new MouseEvent('mousedown', { bubbles: true, button: 0, ...at(from) })
    )
  })
  await act(async () => {
    window.dispatchEvent(
      new MouseEvent('mousemove', { bubbles: true, ...at(to) })
    )
    window.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }))
  })
}

async function renderCanvas(overrides: Partial<RegionCanvasProps> = {}) {
  const props: RegionCanvasProps = {
    tool: 'select',
    draft,
    selected: null,
    onChange: vi.fn(),
    onCreate: vi.fn(),
    onSelect: vi.fn(),
    ...overrides,
  }
  const screen = await render(
    <div
      data-testid='region-surface'
      style={{ width: `${SURFACE_WIDTH}px`, height: `${SURFACE_HEIGHT}px` }}
    >
      <RegionCanvas {...props} />
    </div>
  )
  return { screen, props }
}

describe('RegionCanvas', () => {
  beforeEach(() => {
    injectCanvasSizeCss()
    vi.restoreAllMocks()
  })

  it('renders one labelled rect per draft item with the bottom-center anchor', async () => {
    const { screen } = await renderCanvas()

    await expect.element(screen.getByText('ROI #1')).toBeInTheDocument()
    await expect.element(screen.getByText('MASK #1')).toBeInTheDocument()
    // 每个区域各有一个判定锚点
    expect(screen.getByTitle('底部中心判定锚点').elements()).toHaveLength(2)
  })

  it('selects a rect on mousedown and reports its category and index', async () => {
    const onSelect = vi.fn()
    const { screen } = await renderCanvas({ onSelect })

    await screen.getByText('MASK #1').click()

    expect(onSelect).toHaveBeenCalledWith({ category: 'mask', index: 0 })
  })

  it('clears the selection when the background is clicked with the select tool', async () => {
    const onSelect = vi.fn()
    await renderCanvas({ onSelect, selected: { category: 'roi', index: 0 } })

    await act(async () => {
      canvasRoot().dispatchEvent(
        new MouseEvent('mousedown', {
          bubbles: true,
          button: 0,
          clientX: 0,
          clientY: 0,
        })
      )
    })

    expect(onSelect).toHaveBeenCalledWith(null)
  })

  it('creates a normalized rect when dragging with the ROI tool', async () => {
    const onCreate = vi.fn()
    const onBeforeChange = vi.fn()
    await renderCanvas({
      tool: 'roi',
      onCreate,
      onBeforeChange,
      draft: emptyDraft(),
    })

    await dispatchDrag({ x: 0.1, y: 0.1 }, { x: 0.6, y: 0.6 })

    // 一次拖拽只压一次撤销快照，历史里不堆中间态
    expect(onBeforeChange).toHaveBeenCalledTimes(1)
    expect(onCreate).toHaveBeenCalledTimes(1)
    const [category, rect] = onCreate.mock.calls[0]
    expect(category).toBe('roi')
    // 拖拽起止点换算回归一化坐标（允许浏览器布局取整误差）
    expect(rect[0]).toBeCloseTo(0.1, 2)
    expect(rect[1]).toBeCloseTo(0.1, 2)
    expect(rect[2]).toBeCloseTo(0.6, 2)
    expect(rect[3]).toBeCloseTo(0.6, 2)
  })

  it('ignores sub-minimum drags instead of creating a degenerate rect', async () => {
    const onCreate = vi.fn()
    const onBeforeChange = vi.fn()
    await renderCanvas({
      tool: 'roi',
      onCreate,
      onBeforeChange,
      draft: emptyDraft(),
    })

    // 0.002 归一化边长 < MIN_SIDE_LENGTH (0.01)
    await dispatchDrag({ x: 0.5, y: 0.5 }, { x: 0.502, y: 0.502 })

    expect(onCreate).not.toHaveBeenCalled()
    // 误触不应污染撤销历史
    expect(onBeforeChange).not.toHaveBeenCalled()
  })

  it('does not select or create anything while disabled', async () => {
    const onSelect = vi.fn()
    const onCreate = vi.fn()
    const { screen } = await renderCanvas({
      tool: 'roi',
      disabled: true,
      onSelect,
      onCreate,
    })

    await act(async () => {
      canvasRoot().dispatchEvent(
        new MouseEvent('mousedown', {
          bubbles: true,
          button: 0,
          clientX: 0,
          clientY: 0,
        })
      )
    })
    await screen.getByText('ROI #1').click()

    expect(onCreate).not.toHaveBeenCalled()
    expect(onSelect).not.toHaveBeenCalled()
  })
})
