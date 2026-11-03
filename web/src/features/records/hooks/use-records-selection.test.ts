import { describe, expect, it } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { type RecordsTab } from '../data/schema'
import { useRecordsSelection } from './use-records-selection'

describe('useRecordsSelection', () => {
  const items = [
    { id: '1', name: 'Item 1' },
    { id: '2', name: 'Item 2' },
    { id: '3', name: 'Item 3' },
  ]

  it('initializes with empty selection', async () => {
    const { result } = await renderHook(() =>
      useRecordsSelection('recognitions', items)
    )
    expect(result.current.selectedCount).toBe(0)
    expect(result.current.selectedItems).toEqual([])
    expect(result.current.isSelected('1')).toBe(false)
    expect(result.current.isAllPageSelected).toBe(false)
    expect(result.current.isPartiallySelected).toBe(false)
  })

  it('toggles single item selection', async () => {
    const { result, act } = await renderHook(() =>
      useRecordsSelection('recognitions', items)
    )

    await act(async () => {
      result.current.toggleItem(items[0])
    })

    expect(result.current.selectedCount).toBe(1)
    expect(result.current.isSelected('1')).toBe(true)
    expect(result.current.isPartiallySelected).toBe(true)
    expect(result.current.isAllPageSelected).toBe(false)

    await act(async () => {
      result.current.toggleItem(items[0])
    })

    expect(result.current.selectedCount).toBe(0)
    expect(result.current.isSelected('1')).toBe(false)
  })

  it('selects and deselects all items on current page', async () => {
    const { result, act } = await renderHook(() =>
      useRecordsSelection('recognitions', items)
    )

    await act(async () => {
      result.current.toggleSelectAllCurrentPage()
    })

    expect(result.current.selectedCount).toBe(3)
    expect(result.current.isAllPageSelected).toBe(true)
    expect(result.current.isPartiallySelected).toBe(false)

    await act(async () => {
      result.current.toggleSelectAllCurrentPage()
    })

    expect(result.current.selectedCount).toBe(0)
    expect(result.current.isAllPageSelected).toBe(false)
  })

  it('clears selection when current tab changes (tab isolation)', async () => {
    let currentTab: RecordsTab = 'recognitions'
    const { result, act, rerender } = await renderHook(() =>
      useRecordsSelection(currentTab, items)
    )

    await act(async () => {
      result.current.selectItems([items[0], items[1]])
    })
    expect(result.current.selectedCount).toBe(2)

    // 切换 Tab
    currentTab = 'alarms'
    await rerender()

    expect(result.current.selectedCount).toBe(0)
    expect(result.current.selectedItems).toEqual([])
  })
})
