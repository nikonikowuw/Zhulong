import { act, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { Button } from '@/components/ui/button'
import { useRegionEditor } from '../hooks/use-region-editor'
import { TripwireCanvas } from './tripwire-canvas'
import { TripwireList } from './tripwire-list'

function Editor({ disabled = false }: { disabled?: boolean }) {
  const editor = useRegionEditor()
  const [drawing, setDrawing] = useState(true)
  return (
    <>
      <div style={{ position: 'relative', width: 400, height: 200 }}>
        <TripwireCanvas
          draft={editor.draft}
          drawing={drawing}
          editable={!disabled}
          onChange={editor.changeDraft}
          onBeforeChange={editor.snapshot}
        />
      </div>
      <TripwireList
        draft={editor.draft}
        disabled={disabled}
        onChange={editor.changeDraft}
        onBeforeChange={editor.snapshot}
      />
      <Button onClick={editor.undo}>Undo</Button>
      <Button onClick={() => setDrawing(false)}>Adjust</Button>
      <output>{JSON.stringify(editor.draft.tripwires ?? [])}</output>
    </>
  )
}
async function draw(from: [number, number], to: [number, number]) {
  const svg = document.querySelector('svg')!
  // Synthetic pointer events do not create a native active pointer. Only capture
  // is substituted; real browser layout and coordinate conversion stay intact.
  vi.spyOn(svg, 'setPointerCapture').mockImplementation(() => {})
  svg.style.width = '400px'
  svg.style.height = '200px'
  const box = svg.getBoundingClientRect()
  const event = (type: string, p: [number, number]) =>
    new PointerEvent(type, {
      bubbles: true,
      pointerId: 1,
      button: 0,
      clientX: box.left + p[0] * box.width,
      clientY: box.top + p[1] * box.height,
    })
  await act(async () => {
    svg.dispatchEvent(event('pointerdown', from))
  })
  await act(async () => {
    svg.dispatchEvent(event('pointermove', to))
  })
  await act(async () => {
    svg.dispatchEvent(event('pointerup', to))
  })
}

describe('tripwire editor', () => {
  it('draws full-frame coordinates, switches direction, deletes and undoes', async () => {
    const screen = await render(<Editor />)
    await draw([0.2, 0.5], [0.8, 0.5])
    const output = () =>
      JSON.parse(document.querySelector('output')!.textContent!)
    expect(output()[0].points).toEqual([
      [0.2, 0.5],
      [0.8, 0.5],
    ])
    expect(output()[0].direction).toBe('both')
    const id = output()[0].id
    await screen.getByRole('combobox').click()
    await screen.getByRole('option', { name: 'A → B', exact: true }).click()
    expect(output()[0].direction).toBe('a_to_b')
    expect(output()[0].id).toBe(id)
    await screen.getByRole('button', { name: '删除绊线' }).click()
    expect(output()).toEqual([])
    await screen.getByRole('button', { name: 'Undo' }).click()
    expect(output()[0].id).toBe(id)
    expect(output()[0].direction).toBe('a_to_b')
  })
  it('adjusts endpoints with the keyboard and restores the same line on undo', async () => {
    const screen = await render(<Editor />)
    await draw([0.2, 0.5], [0.8, 0.5])
    await screen.getByRole('button', { name: 'Adjust' }).click()
    const endpoint = document.querySelector('circle')!
    await act(async () => {
      endpoint.dispatchEvent(
        new KeyboardEvent('keydown', { bubbles: true, key: 'ArrowRight' })
      )
    })
    const output = () =>
      JSON.parse(document.querySelector('output')!.textContent!)
    expect(output()[0].points[0][0]).toBeCloseTo(0.21)
    await screen.getByRole('button', { name: 'Undo' }).click()
    expect(output()[0].points[0][0]).toBe(0.2)
  })
  it('rejects a degenerate line', async () => {
    await render(<Editor />)
    await draw([0.2, 0.5], [0.2, 0.5])
    expect(document.querySelector('output')!.textContent).toBe('[]')
  })
  it('rejects short lines and respects the read-only gate', async () => {
    await render(<Editor disabled />)
    await draw([0.2, 0.5], [0.8, 0.5])
    expect(document.querySelector('output')!.textContent).toBe('[]')
  })
})
