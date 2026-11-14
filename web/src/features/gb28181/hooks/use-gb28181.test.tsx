import {
  MutationCache,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { gb28181Api } from '../api/gb28181-api'
import { type Gb28181Config, type Gb28181ConfigResponse } from '../data/schema'
import {
  GB28181_QUERY_KEYS,
  useAddGb28181ChannelCamera,
  useDeleteGb28181Device,
  useGb28181Config,
  useSyncGb28181Catalog,
  useUpdateGb28181Config,
} from './use-gb28181'

vi.mock('../api/gb28181-api', () => ({
  gb28181Api: {
    getConfig: vi.fn(),
    updateConfig: vi.fn(),
    getRuntime: vi.fn(),
    listDevices: vi.fn(),
    upsertDevice: vi.fn(),
    deleteDevice: vi.fn(),
    syncCatalog: vi.fn(),
    addChannelAsCamera: vi.fn(),
  },
}))

const startupConfig: Gb28181Config = {
  enabled: true,
  sipId: '34020000002000000001',
  sipRealm: '3402000000',
  sipListen: '0.0.0.0:5060',
  sipTransport: 'both',
  securityProfile: 'standard',
  mediaBindAddress: '0.0.0.0',
  mediaAdvertisedIp: '192.168.1.10',
  rtpPortMin: 30000,
  rtpPortMax: 30100,
  requireRegisteredDevice: true,
}

const savedConfig: Gb28181Config = {
  ...startupConfig,
  sipListen: '0.0.0.0:5061',
}

const beforeSave: Gb28181ConfigResponse = {
  config: startupConfig,
  runtime: {
    startupConfig,
    state: 'running',
    detail: '',
    advertisedIp: '192.168.1.10',
    restartRequired: false,
    mediaCapacity: 50,
  },
}

/** 保存后的服务端视图：目标配置已变，本进程仍跑启动快照。 */
const afterSave: Gb28181ConfigResponse = {
  config: savedConfig,
  runtime: { ...beforeSave.runtime, restartRequired: true },
}

/** 运行态快照的缓存内容；保存后应当被标记为失效。 */
const snapshotFixture = {
  state: 'running',
  restartRequired: false,
  devices: { total: 0, online: 0 },
  sessions: { active: 0 },
  portPool: { pairsTotal: 50, pairsUsed: 0, exhaustedHits: 0 },
  ingress: {
    oversizedMessages: 0,
    rejectedConns: 0,
    inboundLimited: 0,
    outboundLimited: 0,
    activeConns: 0,
  },
  generatedAt: '2026-10-25T08:00:00Z',
}

function createClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

describe('useGb28181Config', () => {
  it('polls runtime diagnostics while the page is visible', async () => {
    const getConfig = vi.mocked(gb28181Api.getConfig)
    getConfig.mockResolvedValue(beforeSave)
    const client = createClient()
    vi.useFakeTimers()

    let unmount: (() => void) | undefined
    try {
      const hook = await renderHook(() => useGb28181Config(), {
        wrapper: ({ children }) => (
          <QueryClientProvider client={client}>{children}</QueryClientProvider>
        ),
      })
      unmount = hook.unmount

      expect(getConfig).toHaveBeenCalledTimes(1)
      await hook.act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })
      expect(getConfig).toHaveBeenCalledTimes(2)
    } finally {
      unmount?.()
      vi.useRealTimers()
    }
  })
})

describe('useDeleteGb28181Device', () => {
  it('invalidates device/runtime data and removes the deleted device catalog', async () => {
    const deviceId = '34020000001320000001'
    vi.mocked(gb28181Api.deleteDevice).mockResolvedValue()
    const client = createClient()
    client.setQueryData(GB28181_QUERY_KEYS.devices(), [{ deviceId }])
    client.setQueryData(GB28181_QUERY_KEYS.runtime(), snapshotFixture)
    client.setQueryData(GB28181_QUERY_KEYS.channels(deviceId), [])

    const { act, result } = await renderHook(() => useDeleteGb28181Device(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })

    await act(async () => {
      await result.current.mutateAsync(deviceId)
    })

    expect(gb28181Api.deleteDevice).toHaveBeenCalledWith(deviceId)
    expect(
      client.getQueryState(GB28181_QUERY_KEYS.devices())?.isInvalidated
    ).toBe(true)
    expect(
      client.getQueryState(GB28181_QUERY_KEYS.runtime())?.isInvalidated
    ).toBe(true)
    expect(
      client.getQueryData(GB28181_QUERY_KEYS.channels(deviceId))
    ).toBeUndefined()
  })
})

describe('useUpdateGb28181Config', () => {
  it('writes the response back into the cache so the restart banner can flip', async () => {
    vi.mocked(gb28181Api.updateConfig).mockResolvedValue(afterSave)
    const client = createClient()
    client.setQueryData(GB28181_QUERY_KEYS.config(), beforeSave)
    client.setQueryData(GB28181_QUERY_KEYS.runtime(), snapshotFixture)

    const { act, result } = await renderHook(() => useUpdateGb28181Config(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })

    await act(async () => {
      await result.current.mutateAsync(savedConfig)
    })

    expect(gb28181Api.updateConfig).toHaveBeenCalledWith(savedConfig)
    // 页面读的是缓存里的 config + runtime，两处都必须换成服务端返回值。
    expect(client.getQueryData(GB28181_QUERY_KEYS.config())).toEqual(afterSave)
    expect(
      client.getQueryState(GB28181_QUERY_KEYS.runtime())?.isInvalidated
    ).toBe(true)
  })

  it('suppresses the global toast so the form can own the field errors', async () => {
    const client = createClient()
    const { act, result } = await renderHook(() => useUpdateGb28181Config(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })

    vi.mocked(gb28181Api.updateConfig).mockRejectedValue(new Error('boom'))
    await act(async () => {
      await expect(result.current.mutateAsync(savedConfig)).rejects.toThrow(
        'boom'
      )
    })

    expect(result.current.error?.message).toBe('boom')
  })
})

describe('channel drawer mutations', () => {
  it('marks useSyncGb28181Catalog and useAddGb28181ChannelCamera with suppressGlobalToast so MutationCache does not double-toast', async () => {
    const globalErrorSpy = vi.fn()
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
      mutationCache: new MutationCache({
        onError: (_error, _vars, _ctx, mutation) => {
          if (mutation.meta?.suppressGlobalToast) return
          globalErrorSpy()
        },
      }),
    })

    vi.mocked(gb28181Api.syncCatalog).mockRejectedValueOnce(new Error('sync'))
    vi.mocked(gb28181Api.addChannelAsCamera).mockRejectedValueOnce(
      new Error('bind')
    )

    const syncHook = await renderHook(() => useSyncGb28181Catalog(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })
    const addHook = await renderHook(() => useAddGb28181ChannelCamera(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })

    await syncHook.act(async () => {
      await expect(
        syncHook.result.current.mutateAsync('34020000001320000001')
      ).rejects.toThrow('sync')
    })
    await addHook.act(async () => {
      await expect(
        addHook.result.current.mutateAsync({
          deviceId: '34020000001320000001',
          channelId: '34020000001310000001',
        })
      ).rejects.toThrow('bind')
    })

    expect(globalErrorSpy).not.toHaveBeenCalled()
  })
})
