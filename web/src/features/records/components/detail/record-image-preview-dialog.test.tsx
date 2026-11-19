import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import {
  RecordImagePreviewDialog,
  type RecordPreviewImageItem,
} from './record-image-preview-dialog'

// 1x1 真实透明 GIF，可在真实无头浏览器中成功解码加载
const TINY_GIF_DATA_URL =
  'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'

const fullImage: RecordPreviewImageItem = {
  id: 'full',
  url: TINY_GIF_DATA_URL,
  title: '全景图',
  landmarks: [
    [0.2, 0.3],
    [0.3, 0.4],
    [0.4, 0.35],
    [0.5, 0.45],
    [0.6, 0.5],
  ],
  boxes: [
    { box: [0.1, 0.2, 0.4, 0.6], label: 'Alice', variant: 'primary' },
    { box: [0.05, 0.1, 0.8, 0.9], label: '人体', variant: 'secondary' },
  ],
}

describe('RecordImagePreviewDialog', () => {
  it('renders detection boxes and landmarks together', async () => {
    const { getByTestId } = await render(
      <RecordImagePreviewDialog
        images={[fullImage]}
        activeIndex={0}
        onActiveIndexChange={vi.fn()}
      />
    )

    const primary = getByTestId('preview-box-primary').element() as HTMLElement
    expect(primary.style.left).toBe('10%')
    expect(primary.style.top).toBe('20%')
    expect(Number.parseFloat(primary.style.width)).toBeCloseTo(30)
    expect(Number.parseFloat(primary.style.height)).toBeCloseTo(40)
    expect(primary.textContent).toContain('Alice')

    expect(getByTestId('preview-box-secondary').elements()).toHaveLength(1)
    expect(getByTestId('preview-landmark-point').elements()).toHaveLength(5)
  })

  it('hides boxes and landmarks together when showBoxes is false', async () => {
    const { getByTestId } = await render(
      <RecordImagePreviewDialog
        images={[fullImage]}
        activeIndex={0}
        onActiveIndexChange={vi.fn()}
        showBoxes={false}
      />
    )

    expect(getByTestId('preview-box-primary').elements()).toHaveLength(0)
    expect(getByTestId('preview-box-secondary').elements()).toHaveLength(0)
    expect(getByTestId('preview-landmark-point').elements()).toHaveLength(0)
  })

  it('keeps crop landmarks without drawing a detection box', async () => {
    const cropImage: RecordPreviewImageItem = {
      id: 'face-crop',
      url: TINY_GIF_DATA_URL,
      title: '人脸裁剪图',
      landmarks: [[0.5, 0.5]],
    }

    const { getByTestId } = await render(
      <RecordImagePreviewDialog
        images={[cropImage]}
        activeIndex={0}
        onActiveIndexChange={vi.fn()}
      />
    )

    expect(getByTestId('preview-box-primary').elements()).toHaveLength(0)
    expect(getByTestId('preview-landmark-point').elements()).toHaveLength(1)
  })
})
