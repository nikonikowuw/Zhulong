import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { recordsApi } from '../api/records-api'
import { useAlarmTypes } from './use-alarm-types'
import { useAlarms } from './use-alarms'
import { useCaptureTargetTypes } from './use-capture-target-types'
import { useCaptures } from './use-captures'
import { useRecognitions } from './use-recognitions'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

describe('records query hooks enabled guards', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('does not fetch inactive tab queries when params are undefined', async () => {
    const recSpy = vi.spyOn(recordsApi, 'getRecognitions')
    const alarmSpy = vi.spyOn(recordsApi, 'getAlarms')
    const capSpy = vi.spyOn(recordsApi, 'getCaptures')
    const alarmTypesSpy = vi.spyOn(recordsApi, 'getAlarmTypes')
    const targetTypesSpy = vi.spyOn(recordsApi, 'getCaptureTargetTypes')

    const wrapper = createWrapper()

    await renderHook(() => useRecognitions(undefined), { wrapper })
    await renderHook(() => useAlarms(undefined), { wrapper })
    await renderHook(() => useCaptures(undefined), { wrapper })
    await renderHook(() => useAlarmTypes({ enabled: false }), { wrapper })
    await renderHook(() => useCaptureTargetTypes({ enabled: false }), {
      wrapper,
    })

    expect(recSpy).not.toHaveBeenCalled()
    expect(alarmSpy).not.toHaveBeenCalled()
    expect(capSpy).not.toHaveBeenCalled()
    expect(alarmTypesSpy).not.toHaveBeenCalled()
    expect(targetTypesSpy).not.toHaveBeenCalled()
  })

  it('fetches when params are provided or enabled is true', async () => {
    const recSpy = vi.spyOn(recordsApi, 'getRecognitions').mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      pageSize: 20,
    })
    const alarmTypesSpy = vi
      .spyOn(recordsApi, 'getAlarmTypes')
      .mockResolvedValue(['intrusion'])

    const wrapper = createWrapper()

    await renderHook(() => useRecognitions({ page: 1, pageSize: 20 }), {
      wrapper,
    })
    await renderHook(() => useAlarmTypes({ enabled: true }), { wrapper })

    await vi.waitFor(() => {
      expect(recSpy).toHaveBeenCalledTimes(1)
      expect(alarmTypesSpy).toHaveBeenCalledTimes(1)
    })
  })
})
