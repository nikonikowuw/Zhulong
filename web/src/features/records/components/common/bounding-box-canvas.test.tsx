import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { BoundingBoxCanvas } from './bounding-box-canvas'

// 1x1 真实透明 GIF，可在真实无头浏览器中成功解码加载
const TINY_PNG_DATA_URL =
  'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'

describe('BoundingBoxCanvas', () => {
  it('renders image and sets constraint class on img element', async () => {
    const { getByAltText, getByText } = await render(
      <BoundingBoxCanvas
        imagePath={TINY_PNG_DATA_URL}
        alt='Test Image'
        primaryBox={[0.1, 0.2, 0.5, 0.6]}
        primaryLabel='vehicle'
        className='max-h-[460px]'
      />
    )

    const img = getByAltText('Test Image')
    await expect.element(img).toBeInTheDocument()
    // 约束必须作用在 img 元素上，防止父容器截断图像导致覆盖层错位
    await expect.element(img).toHaveClass('max-h-[460px]')
    // 加载完成前处于隐藏过渡状态，并且展示加载指示
    await expect.element(getByText(/正在加载全景图像/)).toBeInTheDocument()
  })

  it('handles image load and renders bounding box overlay with dimensions', async () => {
    const onDimensionsLoaded = vi.fn()
    const { getByAltText, getByText } = await render(
      <BoundingBoxCanvas
        imagePath={TINY_PNG_DATA_URL}
        alt='Inspection Frame'
        primaryBox={[0.2, 0.3, 0.7, 0.8]}
        primaryLabel='Person of Interest'
        onDimensionsLoaded={onDimensionsLoaded}
      />
    )

    const img = getByAltText('Inspection Frame')
    // 在真实无头浏览器中等待 data URL 图片自然加载完成
    await expect.element(img).toHaveClass('opacity-100')
    expect(onDimensionsLoaded).toHaveBeenCalledWith({
      width: 1,
      height: 1,
    })

    // 标注框平滑挂载
    await expect.element(getByText('Person of Interest')).toBeInTheDocument()
  })

  it('handles image load error and renders error feedback', async () => {
    const { getByText, getByRole } = await render(
      <BoundingBoxCanvas
        imagePath='snapshots/definitely-not-found-image-123456.jpg'
        alt='Broken Frame'
      />
    )

    // 真实浏览器遇到 404 会触发 onError，并展示错误提示与重试按钮
    await expect.element(getByText(/全景图像加载失败/)).toBeInTheDocument()
    await expect
      .element(getByRole('button', { name: /重试/ }))
      .toBeInTheDocument()
  })
})
