import { type TFunction } from 'i18next'
import { describe, expect, it } from 'vitest'
import { type Capture, type Recognition } from '../../data/schema'
import { buildPreviewImages } from './record-detail-helpers'

// 只关心 key 的映射，标签文案由 i18n 资源保证
const t = ((key: string) => key) as unknown as TFunction<'records'>

const recognition: Recognition = {
  id: 'rec-1',
  cameraId: 'cam-1',
  algorithmId: 'algo-1',
  pts: 1,
  trackId: 7,
  personId: 'p-1',
  personExternalId: 'EMP-1',
  personName: 'Alice',
  matchedFaceId: 'f-1',
  matched: true,
  similarity: 0.9,
  candidates: [],
  faceImagePath: 'snapshots/rec-1_face.jpg',
  galleryImagePath: '',
  imagePath: 'snapshots/rec-1.jpg',
  box: [0.2, 0.2, 0.4, 0.5],
  bodyBox: [0.1, 0.1, 0.6, 0.9],
  landmarks: [
    [0.25, 0.3],
    [0.3, 0.3],
    [0.28, 0.35],
    [0.26, 0.4],
    [0.3, 0.4],
  ],
  attributes: {},
  capturedAt: '2026-03-31T12:00:00Z',
  createdAt: '2026-03-31T12:00:01Z',
}

const capture: Capture = {
  id: 'cap-1',
  cameraId: 'cam-1',
  targetType: 'person',
  trackId: 3,
  score: 0.9,
  box: [0.1, 0.1, 0.5, 0.9],
  landmarks: [],
  cropPath: 'snapshots/cap-1_crop.jpg',
  imagePath: 'snapshots/cap-1.jpg',
  attributes: {},
  pts: 2,
  capturedAt: '2026-03-31T12:00:00Z',
  createdAt: '2026-03-31T12:00:01Z',
}

describe('buildPreviewImages', () => {
  it('attaches the face and body boxes to the full image only', () => {
    const images = buildPreviewImages(recognition, 'recognitions', t)
    const full = images.find((item) => item.id === 'full')
    expect(full?.boxes).toHaveLength(2)
    expect(full?.boxes?.[0]).toMatchObject({
      box: recognition.box,
      variant: 'primary',
      label: 'Alice',
    })
    expect(full?.boxes?.[1]).toMatchObject({
      box: recognition.bodyBox,
      variant: 'secondary',
      label: 'detail.bodyBoxLabel',
    })

    // 裁剪图只保留投影后的 5 点，不再叠加一个“等于整张裁剪图”的框
    const crop = images.find((item) => item.id === 'face-crop')
    expect(crop?.boxes).toBeUndefined()
    expect(crop?.landmarks).toHaveLength(5)
  })

  it('omits the secondary box when the body box coincides with the face box', () => {
    const degenerate: Recognition = {
      ...recognition,
      bodyBox: recognition.box,
    }
    const images = buildPreviewImages(degenerate, 'recognitions', t)
    expect(images.find((item) => item.id === 'full')?.boxes).toHaveLength(1)
  })

  it('skips the overlay for the zero box of a record without detection', () => {
    const noBox: Recognition = {
      ...recognition,
      box: [0, 0, 0, 0],
      bodyBox: [0, 0, 0, 0],
    }
    const images = buildPreviewImages(noBox, 'recognitions', t)
    expect(images.find((item) => item.id === 'full')?.boxes).toBeUndefined()
  })

  it('attaches a single capture box to the full image, not to the crop', () => {
    const images = buildPreviewImages(capture, 'captures', t)
    const full = images.find((item) => item.id === 'full')
    expect(full?.boxes).toHaveLength(1)
    expect(full?.boxes?.[0]).toMatchObject({
      box: capture.box,
      variant: 'primary',
      label: 'targetTypes.person',
    })
    expect(
      images.find((item) => item.id === 'capture-crop')?.boxes
    ).toBeUndefined()
  })
})
