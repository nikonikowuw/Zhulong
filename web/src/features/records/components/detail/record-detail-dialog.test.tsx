import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { evidenceFileName } from '../../data/evidence-file-name'
import { type Alarm, type Capture, type Recognition } from '../../data/schema'
import { RecordDetailDialog } from './record-detail-dialog'

const mockRecognition: Recognition = {
  id: 'rec-123',
  cameraId: 'cam-gate',
  algorithmId: 'algo-face-v2',
  pts: 55000,
  trackId: 18,
  personId: 'p-888',
  personExternalId: 'EMP-888',
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

const camNames = (cameraId: string) =>
  ({ 'cam-gate': '南门枪机', 'cam-perimeter': '北墙球机' })[cameraId] ??
  (cameraId === 'cam-lane' ? '东门卡口' : undefined)

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

  it('loads the gallery photo from the person endpoint, not the media root', async () => {
    // 回归用例：底库照存放在 facesDir，与 mediafile 托管的媒体根是两个根。
    // 曾经这里把 galleryImagePath（裸文件名）拼成 /api/v1/media/content/...，
    // 被首段白名单以 422 MEDIA_KIND_INVALID 拒绝，详情页永远显示占位图。
    //
    // ⚠️ 断言必须尽早做：真实无头浏览器里图片 404 会触发 onError，MediaImage
    // 随即卸载 <img> 换成占位图标。因此这里捕获首次渲染的 src，而不是等待元素
    // 稳定存在。
    const seenSrcs: string[] = []
    const originalCreateElement = document.createElement.bind(document)
    const spy = vi.spyOn(document, 'createElement').mockImplementation(((
      tag: string,
      options?: ElementCreationOptions
    ) => {
      const el = originalCreateElement(tag, options)
      if (tag === 'img') {
        const setAttribute = el.setAttribute.bind(el)
        el.setAttribute = (name: string, value: string) => {
          if (name === 'src') seenSrcs.push(value)
          setAttribute(name, value)
        }
      }
      return el
    }) as typeof document.createElement)

    const handleOpenChange = vi.fn()
    await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={handleOpenChange}
        record={mockRecognition}
        tab='recognitions'
      />
    )
    spy.mockRestore()

    const gallerySrc = seenSrcs.find((src) => src.includes('/persons/'))
    expect(gallerySrc).toBe(
      `/api/v1/persons/${mockRecognition.personId}/faces/${mockRecognition.matchedFaceId}/image`
    )
    // 底库照绝不能走 media/content 取：那条路径必然 422。
    expect(seenSrcs).not.toContain('/api/v1/media/content/gallery/p-888.jpg')
  })

  it('renders the camera name in the header and metadata strip', async () => {
    const { getByText } = await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={vi.fn()}
        record={mockRecognition}
        tab='recognitions'
        cameraName={camNames}
      />
    )

    // 表头与底部元数据条都应显示名称，任何一处都不应再露出裸 ID。
    await vi.waitFor(() => {
      expect(getByText('南门枪机').elements()).toHaveLength(2)
    })
    expect(getByText('cam-gate').query()).toBeNull()
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

  it('renders vehicle details with full scene, crop, and structured attributes', async () => {
    const vehicleCapture: Capture = {
      ...mockCapture,
      attributes: {
        plate_number: '粤B88888',
        plate_color: 'blue',
        vehicle_type: 'suv',
      },
    }

    const { getByRole, getByText } = await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={vi.fn()}
        record={vehicleCapture}
        tab='vehicles'
      />
    )

    await expect
      .element(getByRole('heading', { name: '车辆结构化抓拍详情' }))
      .toBeInTheDocument()
    await expect.element(getByText('车辆裁剪图')).toBeInTheDocument()
    await expect.element(getByText(/plate_number/)).toBeInTheDocument()
    await vi.waitFor(() => {
      expect(getByText(/粤B88888/).elements().length).toBeGreaterThanOrEqual(1)
    })
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
    await expect.element(getByText('机动车')).toBeInTheDocument()

    const closeBtn = getByRole('button', { name: '关闭' })
    await userEvent.click(closeBtn)
    expect(handleOpenChange).toHaveBeenCalledWith(false)
  })

  it('names the downloaded evidence file after its capture time', async () => {
    // 磁盘名是裸 ULID，且后端刻意不下发 Content-Disposition，所以用户硬盘上
    // 的文件名完全由这里决定。抓拍时刻写进文件名后，下载目录里才排得出先后。
    const { getByRole } = await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={vi.fn()}
        record={mockCapture}
        tab='captures'
      />
    )

    // Radix 的 DialogContent 渲染在 body 的 portal 里，且挂载要等一帧，所以
    // 不能查 render 容器的 container。
    await expect.element(getByRole('dialog')).toBeInTheDocument()
    const anchor = document.querySelector<HTMLAnchorElement>('a[download]')
    expect(anchor).not.toBeNull()
    const expected = evidenceFileName(mockCapture.id, mockCapture.capturedAt)
    expect(anchor?.getAttribute('download')).toBe(expected)
    // 回归：别再退回信息量为零的常量前缀。
    expect(anchor?.getAttribute('download')).not.toContain('capture-')
  })

  it('supports toggling bounding box visibility', async () => {
    const { getByRole } = await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={vi.fn()}
        record={mockRecognition}
        tab='recognitions'
      />
    )

    const toggleBtn = getByRole('button', { name: '隐藏标注框' })
    await expect.element(toggleBtn).toBeInTheDocument()
    await userEvent.click(toggleBtn)

    const showBtn = getByRole('button', { name: '显示标注框' })
    await expect.element(showBtn).toBeInTheDocument()
  })

  it('supports toggling between structured and json code view in attributes panel', async () => {
    const vehicleCapture: Capture = {
      ...mockCapture,
      attributes: {
        plate_number: '粤B88888',
        plate_color: 'blue',
      },
    }

    const { getByRole, getByText } = await render(
      <RecordDetailDialog
        open={true}
        onOpenChange={vi.fn()}
        record={vehicleCapture}
        tab='vehicles'
      />
    )

    const codeBtn = getByRole('button', { name: '代码视图' })
    await userEvent.click(codeBtn)
    await expect
      .element(getByText(/"plate_number": "粤B88888"/))
      .toBeInTheDocument()

    const structBtn = getByRole('button', { name: '结构化视图' })
    await userEvent.click(structBtn)
    await expect.element(getByText('plate_number')).toBeInTheDocument()
  })
})
