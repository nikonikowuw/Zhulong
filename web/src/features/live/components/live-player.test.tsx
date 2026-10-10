import { createRef } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { LivePlayer, type LivePlayerRef } from './live-player'

describe('LivePlayer', () => {
  it('正确挂载组件并提供 ref 方法集合', async () => {
    const playerRef = createRef<LivePlayerRef>()
    const onStatsChange = vi.fn()
    const onError = vi.fn()

    const { container } = await render(
      <LivePlayer
        ref={playerRef}
        onStatsChange={onStatsChange}
        onError={onError}
      />
    )

    expect(container).not.toBeNull()
    expect(playerRef.current).not.toBeNull()
    expect(typeof playerRef.current?.decode).toBe('function')
    expect(typeof playerRef.current?.screenshot).toBe('function')
    expect(typeof playerRef.current?.setMute).toBe('function')
    expect(typeof playerRef.current?.reset).toBe('function')

    // 验证安全调用无异常
    playerRef.current?.setMute(true)
    playerRef.current?.reset()
  })
})
