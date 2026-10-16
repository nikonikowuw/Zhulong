import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Alarm, type Capture, type Recognition } from '../../data/schema'
import { RecordDetailDialog } from './record-detail-dialog'

const mockRecognition: Recognition = {
  id: 'rec-123',
  cameraId: 'cam-gate',
  algorithmId: 'algo-face-v2',
  pts: 55000,
  trackId: 18,
  personId: 'p-888',
  personName: 'Alice Zhang',
  matchedFaceId: 'f-1',
  matched: true,
  similarity: 0.94,
  candidates: [
    {
      personId: 'p-888',
      personName: 'Alice Zhang',
      faceId: 'f-1',
      similarity: 0.94,
    },
  ],
  faceImagePath: 'snapshots/face-1.jpg',
  galleryImagePath: 'gallery/p-888.jpg',
  imagePath: 'snapshots/full-1.jpg',
  box: [0.1, 0.1, 0.3, 0.3],
  bodyBox: [0.05, 0.05, 0.5, 0.9],
  attributes: { age: 26, mask: false },
  capturedAt: '2026-03-31T12:00:00Z',
  createdAt: '2026-03-31T12:00:01Z',
}

const mockAlarm: Alarm = {
  id: 'alarm-456',
  cameraId: 'cam-perimeter',
  algorithmId: 'algo-intrusion',
  alarmType: 'tripwire',
  score: 0.89,
  box: [0.2, 0.2, 0.4, 0.5],
  imagePath: 'snapshots/alarm-full.jpg',
  cropPath: 'snapshots/alarm-crop.jpg',
  extra: { zone: 'north_fence', speed: 1.4 },
  pts: 66000,
  capturedAt: '2026-03-31T12:30:00Z',
  createdAt: '2026-03-31T12:30:01Z',
}

const mockCapture: Capture = {
  id: 'cap-789',
  cameraId: 'cam-lane',
  targetType: 'vehicle',
  trackId: 44,
  score: 0.96,
  box: [0.15, 0.2, 0.6, 0.7],
  cropPath: 'snapshots/vehicle-crop.jpg',
  imagePath: 'snapshots/vehicle-full.jpg',
  attributes: { color: 'white' },
  pts: 77000,
  capturedAt: '2026-03-31T13:00:00Z',
  createdAt: '2026-03-31T13:00:01Z',
}

describe('RecordDetailDialog', () => {
  it('renders recognition details correctly', async () => {
    const handleOpenChange = vi.fn()
    const { getByRole, getByText } = await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={handleOpenChange}
        record={mockRecognition}
        tab='recognitions'
      />
    )

    await expect.element(getByRole('dialog')).toBeInTheDocument()
    await expect
      .element(getByRole('heading', { name: /人脸识别研判详情/ }))
      .toBeInTheDocument()
    await expect.element(getByText('Top-N 候选人比对')).toBeInTheDocument()
  })

  it('renders alarm details and extra parameters', async () => {
    const handleOpenChange = vi.fn()
    const { getByText, getByRole } = await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={handleOpenChange}
        record={mockAlarm}
        tab='alarms'
      />
    )

    await expect.element(getByRole('dialog')).toBeInTheDocument()
    await expect
      .element(getByRole('heading', { name: /事件告警研判详情/ }))
      .toBeInTheDocument()
    await expect.element(getByText('tripwire')).toBeInTheDocument()
  })

  it('renders capture details and closes dialog on close button click', async () => {
    const handleOpenChange = vi.fn()
    const { getByText, getByRole } = await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={handleOpenChange}
        record={mockCapture}
        tab='captures'
      />
    )

    await expect.element(getByRole('dialog')).toBeInTheDocument()
    await expect.element(getByText('vehicle')).toBeInTheDocument()

    const closeBtn = getByRole('button', { name: '关闭' })
    await userEvent.click(closeBtn)
    expect(handleOpenChange).toHaveBeenCalledWith(false)
  })
})
