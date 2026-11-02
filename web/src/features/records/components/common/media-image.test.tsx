import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import { MediaImage } from './media-image'

const TINY_PNG_DATA_URL =
  'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'

describe('MediaImage', () => {
  it('renders fallback placeholder when path and src are missing', async () => {
    const { getByRole, getByText } = await render(
      <MediaImage fallbackText='No Media Provided' />
    )

    await expect.element(getByRole('img')).toBeInTheDocument()
    await expect.element(getByText('No Media Provided')).toBeInTheDocument()
  })

  it('renders image and transitions to loaded state with valid image', async () => {
    const { getByAltText } = await render(
      <MediaImage path={TINY_PNG_DATA_URL} alt='Valid Crop' />
    )

    const img = getByAltText('Valid Crop')
    await expect.element(img).toBeInTheDocument()
    await expect.element(img).toHaveClass('opacity-100')
  })

  it('renders error placeholder when image fails to load', async () => {
    const { getByRole, getByText } = await render(
      <MediaImage
        path='snapshots/broken-crop-999.jpg'
        alt='Broken Media'
        fallbackText='Crop Unavailable'
      />
    )

    // 404 触发 onError 后替换为带 fallbackText 的占位图标
    await expect.element(getByRole('img')).toBeInTheDocument()
    await expect.element(getByText('Crop Unavailable')).toBeInTheDocument()
  })
})
