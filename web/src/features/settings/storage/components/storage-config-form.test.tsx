import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  type ConnectionTestResponse,
  type MigrationJobResponse,
  type StorageConfig,
  type StorageStatus,
} from '../types'
import { StorageConfigForm } from './storage-config-form'
import { StorageMigrationCard } from './storage-migration-card'

const config: StorageConfig = {
  mediaDirectory: 'data/media',
  recordingsRetentionDays: 15,
  snapshotsRetentionDays: 90,
  exportsRetentionHours: 48,
  highWatermarkPercent: 90,
  lowWatermarkPercent: 80,
  emergencyStopPercent: 95,
  emergencyStopMinMb: 2048,
}

const s3Config: StorageConfig = {
  backend: 's3',
  deliveryMode: 'proxy',
  localFallback: true,
  mediaDirectory: 'data/media',
  spoolDirectory: 'data/spool',
  spoolMaxMb: 4096,
  recordingsRetentionDays: 15,
  snapshotsRetentionDays: 90,
  exportsRetentionHours: 48,
  highWatermarkPercent: 90,
  lowWatermarkPercent: 80,
  emergencyStopPercent: 95,
  emergencyStopMinMb: 2048,
  s3: {
    endpoint: 'http://minio.local:9000',
    region: 'us-east-1',
    bucket: 'zhulong-media',
    prefix: 'edge-01',
    usePathStyle: true,
    useTls: false,
    insecureSkipVerify: false,
    accessKeyId: 'minioadmin',
    hasSecretAccessKey: true,
    quotaGb: 100,
  },
}

// 回归用例：原实现是 `grid sm:grid-cols-3`，网格行高取最高单元格，而 grid item
// 默认 stretch，`grid gap-2` 的子项被摊开 —— AI 抓拍列说明文字长出一行，
// 其输入框就被推到下方（修复前实测输入框顶边为 251 / 353 / 475）。
// 现在改为一排 flex 列 + items-start，锁住三列输入框顶边一致。
// 真实页面是 1280x896 桌面宽屏，故用 chromium-desktop 视口。
function renderForm(initialConfig: StorageConfig = config) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <div className='w-[760px]'>
        <StorageConfigForm initialConfig={initialConfig} />
      </div>
    </QueryClientProvider>
  )
}

function makeAxiosOk<T>(data: T) {
  return {
    data: { code: 'OK', message: 'ok', data } satisfies ApiResponse<T>,
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as never,
  }
}

describe('StorageConfigForm retention grid', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('aligns the three retention inputs on the same baseline', async () => {
    const screen = await renderForm()
    await screen.getByText('视频录像保留天数').element()

    // 前三个数字框依次是：录像天数 / 抓拍天数 / 导出小时
    const inputs = (await screen.getByRole('spinbutton').all()).slice(0, 3)
    const rects = inputs.map((input) => input.element().getBoundingClientRect())

    expect(rects).toHaveLength(3)
    const tops = new Set(rects.map((r) => Math.round(r.top)))
    expect(tops.size, `input tops: ${rects.map((r) => r.top)}`).toBe(1)
    // 同排：三列不应互相挤压成一行内叠放
    const lefts = new Set(rects.map((r) => Math.round(r.left)))
    expect(lefts.size).toBe(3)
  })

  it('switches from local to S3 backend, tests connection, and requires secret on first setup', async () => {
    const connResp: ConnectionTestResponse = {
      backend: 's3',
      reachable: true,
      writable: true,
    }
    const postSpy = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValueOnce(makeAxiosOk(connResp))

    const screen = await renderForm(config)

    // Switch to S3 mode
    await screen.getByRole('radio', { name: /MinIO/ }).click()

    const endpointInput = screen.getByPlaceholder(/127\.0\.0\.1:9000/)
    await endpointInput.fill('http://127.0.0.1:9000')

    const bucketInput = screen.getByPlaceholder('例如 zhulong-media')
    await bucketInput.fill('edge-bucket')

    const akInput = screen.getByLabelText('Access Key ID')
    await akInput.fill('minioadmin')

    await screen.getByRole('button', { name: '测试连通性' }).click()

    await expect
      .element(screen.getByText('对象存储连通且具备读写权限'))
      .toBeInTheDocument()
    expect(postSpy).toHaveBeenCalledWith(
      '/system/storage/test-connection',
      expect.objectContaining({
        backend: 's3',
        s3: expect.objectContaining({
          endpoint: 'http://127.0.0.1:9000',
          bucket: 'edge-bucket',
          accessKeyId: 'minioadmin',
        }),
      })
    )

    // Submitting without secretAccessKey on first S3 setup must show Zod validation error
    await screen.getByRole('button', { name: '保存配置' }).click()
    await expect
      .element(screen.getByText('首次启用 S3 必须输入 Secret Access Key'))
      .toBeInTheDocument()
  })

  it('preserves existing encrypted S3 secret when submitted blank', async () => {
    const updatedStatus: StorageStatus = {
      backend: 's3',
      mediaDirectory: 'data/media',
      mountPoint: '/data',
      fsType: 'ext4',
      totalBytes: 1000,
      usedBytes: 100,
      freeBytes: 900,
      usagePercent: 10,
      breakdown: {
        recordingsBytes: 50,
        snapshotsBytes: 25,
        exportsBytes: 0,
        otherBytes: 25,
      },
      status: 'healthy',
      deviceId: 1,
      isExternal: false,
      canWrite: true,
      remoteHealthy: true,
      spoolDirectory: 'data/spool',
      updatedAt: '2026-11-22T08:00:00Z',
    }
    const putSpy = vi
      .spyOn(apiClient, 'put')
      .mockResolvedValueOnce(makeAxiosOk(updatedStatus))

    const screen = await renderForm(s3Config)

    await expect
      .element(screen.getByText('已保存加密密钥 — 留空则保持原密钥不变'))
      .toBeInTheDocument()

    // Leave secret blank, submit directly
    await screen.getByRole('button', { name: '保存配置' }).click()

    await expect.poll(() => putSpy.mock.calls.length).toBe(1)
    const sentPayload = putSpy.mock.calls[0]?.[1] as {
      backend: string
      s3?: { secretAccessKey?: string; hasSecretAccessKey?: boolean }
    }
    expect(sentPayload.backend).toBe('s3')
    expect(sentPayload.s3?.secretAccessKey).toBeUndefined()
    expect(sentPayload.s3?.hasSecretAccessKey).toBe(true)
  })

  it('normalizes deliveryMode to proxy when switching from redirect S3 back to local', async () => {
    const updatedStatus: StorageStatus = {
      backend: 'local',
      mediaDirectory: 'data/media',
      mountPoint: '/data',
      fsType: 'ext4',
      totalBytes: 1000,
      usedBytes: 100,
      freeBytes: 900,
      usagePercent: 10,
      breakdown: {
        recordingsBytes: 50,
        snapshotsBytes: 25,
        exportsBytes: 0,
        otherBytes: 25,
      },
      status: 'healthy',
      deviceId: 1,
      isExternal: false,
      canWrite: true,
      remoteHealthy: true,
      spoolDirectory: 'data/spool',
      updatedAt: '2026-11-22T08:00:00Z',
    }
    const putSpy = vi
      .spyOn(apiClient, 'put')
      .mockResolvedValueOnce(makeAxiosOk(updatedStatus))

    const screen = await renderForm({
      ...s3Config,
      deliveryMode: 'redirect',
    })

    await screen.getByRole('radio', { name: /本地文件系统/ }).click()
    await screen.getByRole('button', { name: '保存配置' }).click()

    await expect.poll(() => putSpy.mock.calls.length).toBe(1)
    const sentPayload = putSpy.mock.calls[0]?.[1] as {
      backend: string
      deliveryMode?: string
    }
    expect(sentPayload.backend).toBe('local')
    expect(sentPayload.deliveryMode).toBe('proxy')
  })
})

describe('StorageMigrationCard', () => {
  it('displays latest migration status and submits POST /system/storage/migrate', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(makeAxiosOk(null))
    const createdJob: MigrationJobResponse = {
      jobId: 'job-mig-1',
      status: 'COMPLETED',
      progress: 100,
      total: 4,
      processed: 4,
      succeeded: 4,
      failed: 0,
      deleteLocal: false,
      createdAt: '2026-11-22T08:00:00Z',
      updatedAt: '2026-11-22T08:00:01Z',
      finishedAt: '2026-11-22T08:00:01Z',
    }
    const postSpy = vi
      .spyOn(apiClient, 'post')
      .mockResolvedValueOnce(makeAxiosOk(createdJob))

    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    const screen = await render(
      <QueryClientProvider client={client}>
        <StorageMigrationCard enabled />
      </QueryClientProvider>
    )

    await screen.getByRole('button', { name: '开始历史资产迁移' }).click()

    await expect.element(screen.getByText('已完成')).toBeInTheDocument()
    await expect
      .element(screen.getByText('已处理 4/4 · 成功 4 · 失败 0'))
      .toBeInTheDocument()
    expect(postSpy).toHaveBeenCalledWith('/system/storage/migrate', {
      deleteLocal: false,
    })
  })
})
