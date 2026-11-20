import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import { type StorageConfig } from '../types'
import { StorageConfigForm } from './storage-config-form'

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

// 回归用例：原实现是 `grid sm:grid-cols-3`，网格行高取最高单元格，而 grid item
// 默认 stretch，`grid gap-2` 的子项被摊开 —— AI 抓拍列说明文字长出一行，
// 其输入框就被推到下方（修复前实测输入框顶边为 251 / 353 / 475）。
// 现在改为一排 flex 列 + items-start，锁住三列输入框顶边一致。
// 真实页面是 1280x896 桌面宽屏，故用 chromium-desktop 视口。
function renderForm() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <div className='w-[760px]'>
        <StorageConfigForm initialConfig={config} />
      </div>
    </QueryClientProvider>
  )
}

describe('StorageConfigForm retention grid', () => {
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
})
