import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Gb28181Config, type Gb28181RuntimeInfo } from '../data/schema'
import { Gb28181ConfigForm } from './gb28181-config-form'

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

const runningRuntime: Gb28181RuntimeInfo = {
  startupConfig,
  state: 'running',
  detail: '',
  advertisedIp: '192.168.1.10',
  restartRequired: false,
  mediaCapacity: 50,
}

describe('Gb28181ConfigForm', () => {
  it('does not claim a restart is pending while desired equals startup', async () => {
    const { container, getByText } = await render(
      <Gb28181ConfigForm
        config={startupConfig}
        runtime={runningRuntime}
        isSaving={false}
        onSave={vi.fn()}
      />
    )

    await expect.element(getByText('启用 GB28181 接入')).toBeInTheDocument()
    // 端口区间 → 50 对 RTP/RTCP
    await expect
      .element(getByText(/当前区间可支撑 50 路并发会话/))
      .toBeInTheDocument()
    expect(container.textContent).not.toContain('已保存，需重启生效')
  })

  it('compares the saved target against the startup configuration', async () => {
    const savedConfig: Gb28181Config = {
      ...startupConfig,
      sipListen: '0.0.0.0:5061',
    }

    const { getByRole, getByText } = await render(
      <Gb28181ConfigForm
        config={savedConfig}
        runtime={{ ...runningRuntime, restartRequired: true }}
        isSaving={false}
        onSave={vi.fn()}
      />
    )

    await expect.element(getByText('已保存，需重启生效')).toBeInTheDocument()
    await expect.element(getByText('运行值')).toBeInTheDocument()
    await expect.element(getByText('待生效值')).toBeInTheDocument()
    // 差异表同时列出运行值与待生效值（表单输入框的 value 不参与文本匹配）
    await expect
      .element(getByRole('cell', { name: '0.0.0.0:5060' }))
      .toBeInTheDocument()
    await expect
      .element(getByRole('cell', { name: '0.0.0.0:5061' }))
      .toBeInTheDocument()
  })

  it('submits the complete payload with the P0-fixed fields pinned', async () => {
    const onSave = vi.fn().mockResolvedValue({
      config: startupConfig,
      runtime: runningRuntime,
    })

    const { getByRole } = await render(
      <Gb28181ConfigForm
        config={startupConfig}
        runtime={runningRuntime}
        isSaving={false}
        onSave={onSave}
      />
    )

    await userEvent.click(getByRole('button', { name: /保存配置/ }))

    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        sipId: '34020000002000000001',
        sipListen: '0.0.0.0:5060',
        securityProfile: 'standard',
        requireRegisteredDevice: true,
      })
    )
  })

  it('blocks the save and reports the field when the identity is empty', async () => {
    const onSave = vi.fn()

    const { getByLabelText, getByRole, getByText } = await render(
      <Gb28181ConfigForm
        config={startupConfig}
        runtime={runningRuntime}
        isSaving={false}
        onSave={onSave}
      />
    )

    const sipIdInput = getByLabelText('SIP 服务端 ID')
    await userEvent.clear(sipIdInput)
    await userEvent.click(getByRole('button', { name: /保存配置/ }))

    expect(onSave).not.toHaveBeenCalled()
    await expect
      .element(getByText('启用时必须填写 SIP 服务端 ID'))
      .toBeInTheDocument()
  })

  it('disables media and port fields when GB28181 is disabled', async () => {
    const { getByLabelText } = await render(
      <Gb28181ConfigForm
        config={{ ...startupConfig, enabled: false }}
        runtime={{ ...runningRuntime, state: 'disabled' }}
        isSaving={false}
        onSave={vi.fn()}
      />
    )

    await expect.element(getByLabelText('媒体绑定地址')).toBeDisabled()
    await expect.element(getByLabelText('媒体通告地址')).toBeDisabled()
    await expect.element(getByLabelText('RTP 起始端口')).toBeDisabled()
    await expect.element(getByLabelText('RTP 结束端口')).toBeDisabled()
  })
})
