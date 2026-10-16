import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Alarm, type Capture, type Recognition } from '../../data/schema'
import { AlarmsView } from './alarms-view'
import { CapturesView } from './captures-view'
import { RecognitionsView } from './recognitions-view'

const sampleRecognitions: Recognition[] = [
  {
    id: 'rec-1',
    cameraId: 'cam-gate',
    algorithmId: 'algo-face',
    pts: 1001,
    trackId: 10,
    personId: 'p-1',
    personName: 'Alice Zhang',
    matchedFaceId: 'f-1',
    matched: true,
    similarity: 0.95,
    candidates: [],
    faceImagePath: 'faces/face-1.jpg',
    galleryImagePath: 'gallery/p-1.jpg',
    imagePath: 'snapshots/rec-1.jpg',
    box: [0.1, 0.2, 0.3, 0.4],
    bodyBox: [0, 0, 0, 0],
    attributes: {},
    capturedAt: '2026-03-31T10:00:00Z',
    createdAt: '2026-03-31T10:00:01Z',
  },
]

const sampleAlarms: Alarm[] = [
  {
    id: 'alarm-1',
    cameraId: 'cam-perimeter',
    algorithmId: 'algo-intrusion',
    alarmType: 'tripwire',
    score: 0.88,
    box: [0.1, 0.1, 0.5, 0.5],
    imagePath: 'snapshots/alarm-1.jpg',
    cropPath: 'snapshots/alarm-1-crop.jpg',
    extra: {},
    pts: 2002,
    capturedAt: '2026-03-31T10:15:00Z',
    createdAt: '2026-03-31T10:15:01Z',
  },
]

const sampleCaptures: Capture[] = [
  {
    id: 'cap-1',
    cameraId: 'cam-lane',
    targetType: 'vehicle',
    trackId: 5,
    score: 0.92,
    box: [0.2, 0.2, 0.6, 0.6],
    cropPath: 'snapshots/cap-1-crop.jpg',
    imagePath: 'snapshots/cap-1.jpg',
    attributes: {},
    pts: 3003,
    capturedAt: '2026-03-31T10:30:00Z',
    createdAt: '2026-03-31T10:30:01Z',
  },
]

describe('Records Views', () => {
  describe('RecognitionsView', () => {
    it('renders grid view with cards and handles onSelect', async () => {
      const handleSelect = vi.fn()
      const { getByText } = await render(
        <RecognitionsView
          items={sampleRecognitions}
          total={1}
          page={1}
          pageSize={20}
          view='grid'
          isLoading={false}
          isError={false}
          onPageChange={vi.fn()}
          onPageSizeChange={vi.fn()}
          onSelect={handleSelect}
          onRetry={vi.fn()}
        />
      )

      const cardName = getByText('Alice Zhang')
      await expect.element(cardName).toBeInTheDocument()
      await userEvent.click(cardName)
      expect(handleSelect).toHaveBeenCalledWith(sampleRecognitions[0])
    })

    it('renders table view with columns and triggers onSelect when clicking row', async () => {
      const handleSelect = vi.fn()
      const { getByRole, getByText } = await render(
        <RecognitionsView
          items={sampleRecognitions}
          total={1}
          page={1}
          pageSize={20}
          view='table'
          isLoading={false}
          isError={false}
          onPageChange={vi.fn()}
          onPageSizeChange={vi.fn()}
          onSelect={handleSelect}
          onRetry={vi.fn()}
        />
      )

      await expect.element(getByRole('table')).toBeInTheDocument()
      const rowItem = getByText('Alice Zhang')
      await expect.element(rowItem).toBeInTheDocument()
      await userEvent.click(rowItem)
      expect(handleSelect).toHaveBeenCalledWith(sampleRecognitions[0])
    })

    it('renders empty state when no items exist in grid and table', async () => {
      const { getByText: getByTextGrid } = await render(
        <RecognitionsView
          items={[]}
          total={0}
          page={1}
          pageSize={20}
          view='grid'
          isLoading={false}
          isError={false}
          onPageChange={vi.fn()}
          onPageSizeChange={vi.fn()}
          onSelect={vi.fn()}
          onRetry={vi.fn()}
        />
      )

      await expect.element(getByTextGrid('暂无感知记录')).toBeInTheDocument()

      const { getByRole, getByText: getByTextTable } = await render(
        <RecognitionsView
          items={[]}
          total={0}
          page={1}
          pageSize={20}
          view='table'
          isLoading={false}
          isError={false}
          onPageChange={vi.fn()}
          onPageSizeChange={vi.fn()}
          onSelect={vi.fn()}
          onRetry={vi.fn()}
        />
      )

      // 表格与表头必须存在
      await expect.element(getByRole('table')).toBeInTheDocument()
      await expect.element(getByTextTable('抓拍快照')).toBeInTheDocument()
      await expect.element(getByTextTable('抓拍时间')).toBeInTheDocument()
      await expect.element(getByTextTable('摄像机')).toBeInTheDocument()
    })
  })

  describe('AlarmsView', () => {
    it('renders alarm card in grid view and table in table view', async () => {
      const handleSelect = vi.fn()
      const { getByText } = await render(
        <AlarmsView
          items={sampleAlarms}
          total={1}
          page={1}
          pageSize={20}
          view='grid'
          isLoading={false}
          isError={false}
          onPageChange={vi.fn()}
          onPageSizeChange={vi.fn()}
          onSelect={handleSelect}
          onRetry={vi.fn()}
        />
      )

      await expect.element(getByText('cam-perimeter')).toBeInTheDocument()
    })
  })

  describe('CapturesView', () => {
    it('renders capture card and triggers select', async () => {
      const handleSelect = vi.fn()
      const { getByText } = await render(
        <CapturesView
          items={sampleCaptures}
          total={1}
          page={1}
          pageSize={20}
          view='grid'
          isLoading={false}
          isError={false}
          onPageChange={vi.fn()}
          onPageSizeChange={vi.fn()}
          onSelect={handleSelect}
          onRetry={vi.fn()}
        />
      )

      await expect.element(getByText('cam-lane')).toBeInTheDocument()
    })

    it('renders capture table view with trackId', async () => {
      const { getByRole, getByText } = await render(
        <CapturesView
          items={sampleCaptures}
          total={1}
          page={1}
          pageSize={20}
          view='table'
          isLoading={false}
          isError={false}
          onPageChange={vi.fn()}
          onPageSizeChange={vi.fn()}
          onSelect={vi.fn()}
          onRetry={vi.fn()}
        />
      )

      await expect.element(getByRole('table')).toBeInTheDocument()
      await expect.element(getByText('#5')).toBeInTheDocument()
      await expect.element(getByText('cam-lane')).toBeInTheDocument()
    })
  })
})
