import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { ListPage } from './list-page'

// 应用顶栏里的 ProfileDropdown / Search 依赖路由与命令面板上下文，和本组件要守的
// 「标题行骨架」无关；替换成最小 header，测试只断言结构与插槽。
vi.mock('@/components/layout/header', () => ({
  Header: ({ fixed }: { fixed?: boolean }) => (
    <header data-testid='app-header' data-fixed={String(fixed)} />
  ),
}))

describe('ListPage', () => {
  it('固定标题行结构并渲染标题、描述与主操作插槽', async () => {
    const { container } = await render(
      <ListPage
        title='人员管理'
        description='维护系统人员档案库'
        actions={<button type='button'>新建</button>}
      >
        <div>页面内容</div>
      </ListPage>
    )

    expect(container.querySelector('[data-testid="app-header"]')).not.toBeNull()
    expect(
      container
        .querySelector('[data-testid="app-header"]')
        ?.getAttribute('data-fixed')
    ).toBe('true')

    const header = container.querySelector('[data-slot="list-page-header"]')
    expect(header).not.toBeNull()
    // 长描述不得把主操作挤到第二行最左：两端对齐 + 允许换行是硬约束。
    expect(header?.className).toContain('flex-wrap')
    expect(header?.className).toContain('items-end')
    expect(header?.className).toContain('justify-between')

    const heading = container.querySelector('[data-slot="list-page-heading"]')
    expect(heading?.className).toContain('min-w-0')
    expect(heading?.className).toContain('flex-1')

    const description = container.querySelector(
      '[data-slot="list-page-heading"] p'
    )
    expect(description?.textContent).toBe('维护系统人员档案库')
    expect(description?.className).toContain('max-w-3xl')

    const actions = container.querySelector('[data-slot="list-page-actions"]')
    expect(actions).not.toBeNull()
    expect(actions?.textContent).toBe('新建')

    expect(container.textContent).toContain('页面内容')
  })

  it('未传 description / actions 时不渲染对应节点', async () => {
    const { container } = await render(
      <ListPage title='审计日志'>
        <div>页面内容</div>
      </ListPage>
    )

    expect(container.textContent).toContain('审计日志')
    expect(
      container.querySelector('[data-slot="list-page-heading"] p')
    ).toBeNull()
    expect(
      container.querySelector('[data-slot="list-page-actions"]')
    ).toBeNull()
  })
})
