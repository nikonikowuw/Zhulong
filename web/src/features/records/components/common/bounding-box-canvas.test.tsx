import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import { BoundingBoxCanvas } from './bounding-box-canvas'

describe('BoundingBoxCanvas', () => {
  it('renders image and sets constraint class on img element', async () => {
    const { getByAltText } = await render(
      <BoundingBoxCanvas
        imagePath='snapshots/test.jpg'
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
  })
})
