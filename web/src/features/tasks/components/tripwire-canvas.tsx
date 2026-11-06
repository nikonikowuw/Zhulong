import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { categoryTextClass } from '@/lib/category-palette'
import {
  MAX_TRIPWIRES,
  MIN_LINE_LENGTH,
  tripwireSchema,
  type RegionDraft,
  type Tripwire,
} from '../data/region-config'

type Point = Tripwire['points'][0]
interface Props {
  draft: RegionDraft
  drawing: boolean
  editable: boolean
  onChange: (draft: RegionDraft) => void
  onBeforeChange?: () => void
}
interface Drag {
  id: string | null
  endpoint: 0 | 1
  points: Tripwire['points']
}

function TripwireGraphic({
  line,
  preview = false,
  width,
  height,
  marker,
  editable,
  drawing,
  onEndpoint,
  onNudge,
}: {
  line: Tripwire
  preview?: boolean
  width: number
  height: number
  marker: string
  editable: boolean
  drawing: boolean
  onEndpoint: (event: React.PointerEvent<SVGElement>, drag: Drag) => void
  onNudge: (id: string, endpoint: 0 | 1, dx: number, dy: number) => void
}) {
  const { t } = useTranslation('tasks')
  const [a, b] = line.points
  // Normal is calculated in physical pixels so arrows stay perpendicular on wide video.
  const dx = (b[0] - a[0]) * width,
    dy = (b[1] - a[1]) * height
  const length = Math.hypot(dx, dy) || 1
  const nx = ((-dy / length) * 24) / width,
    ny = ((dx / length) * 24) / height
  const mx = (a[0] + b[0]) / 2,
    my = (a[1] + b[1]) / 2
  return (
    <g key={line.id}>
      <line
        x1={a[0] * width}
        y1={a[1] * height}
        x2={b[0] * width}
        y2={b[1] * height}
        stroke='currentColor'
        strokeWidth='3'
        vectorEffect='non-scaling-stroke'
        strokeDasharray={preview ? '5 5' : undefined}
      />
      <line
        x1={(mx - nx) * width}
        y1={(my - ny) * height}
        x2={(mx + nx) * width}
        y2={(my + ny) * height}
        stroke='currentColor'
        strokeWidth='2'
        vectorEffect='non-scaling-stroke'
        markerEnd={line.direction !== 'b_to_a' ? `url(#${marker})` : undefined}
        markerStart={
          line.direction !== 'a_to_b' ? `url(#${marker})` : undefined
        }
      />
      <text
        x={(mx - nx * 1.7) * width}
        y={(my - ny * 1.7) * height}
        fill='currentColor'
        fontSize='12'
      >
        A
      </text>
      <text
        x={(mx + nx * 1.7) * width}
        y={(my + ny * 1.7) * height}
        fill='currentColor'
        fontSize='12'
      >
        B
      </text>
      <text
        x={a[0] * width}
        y={a[1] * height - 12}
        fill='currentColor'
        fontSize='12'
      >
        {line.name}
      </text>
      {!preview &&
        editable &&
        !drawing &&
        ([0, 1] as const).map((endpoint) => (
          <circle
            key={endpoint}
            cx={line.points[endpoint][0] * width}
            cy={line.points[endpoint][1] * height}
            r='7'
            fill='currentColor'
            className='pointer-events-auto cursor-move focus:stroke-foreground focus:outline-none'
            tabIndex={0}
            role='button'
            onKeyDown={(event) => {
              const offsets: Record<string, [number, number]> = {
                ArrowLeft: [-0.01, 0],
                ArrowRight: [0.01, 0],
                ArrowUp: [0, -0.01],
                ArrowDown: [0, 0.01],
              }
              const offset = offsets[event.key]
              if (offset) {
                event.preventDefault()
                event.stopPropagation()
                onNudge(line.id, endpoint, ...offset)
              }
            }}
            aria-label={t('region.tripwireEndpoint', {
              name: line.name,
              endpoint: endpoint + 1,
            })}
            onPointerDown={(event) =>
              onEndpoint(event, { id: line.id, endpoint, points: line.points })
            }
          />
        ))}
    </g>
  )
}

/** Endpoint ordering is stable: negative screen-space cross product is A. */
export function TripwireCanvas({
  draft,
  drawing,
  editable,
  onChange,
  onBeforeChange,
}: Props) {
  const { t } = useTranslation('tasks')
  const marker = useId().replace(/:/g, '')
  const svgRef = useRef<SVGSVGElement>(null)
  const [drag, setDrag] = useState<Drag | null>(null)
  const [size, setSize] = useState({ width: 1000, height: 1000 })
  useEffect(() => {
    const node = svgRef.current
    if (!node) return
    const observer = new ResizeObserver(([entry]) =>
      setSize({
        width: entry.contentRect.width || 1000,
        height: entry.contentRect.height || 1000,
      })
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [])
  const lines = draft.tripwires ?? []
  const point = (event: React.PointerEvent<SVGElement>): Point => {
    const rect = svgRef.current!.getBoundingClientRect()
    return [
      Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width)),
      Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height)),
    ]
  }
  const begin = (event: React.PointerEvent<SVGElement>, next: Drag) => {
    if (!editable || event.button !== 0) return
    event.stopPropagation()
    svgRef.current?.setPointerCapture(event.pointerId)
    setDrag(next)
  }

  const nudge = (id: string, endpoint: 0 | 1, dx: number, dy: number) => {
    if (!editable) return
    const line = lines.find((item) => item.id === id)
    if (!line) return
    const points: Tripwire['points'] = [...line.points]
    points[endpoint] = [
      Math.max(0, Math.min(1, points[endpoint][0] + dx)),
      Math.max(0, Math.min(1, points[endpoint][1] + dy)),
    ]
    const next = { ...line, points }
    if (!tripwireSchema.safeParse(next).success) return
    onBeforeChange?.()
    onChange({
      ...draft,
      tripwires: lines.map((item) => (item.id === id ? next : item)),
    })
  }

  return (
    <svg
      ref={svgRef}
      viewBox={`0 0 ${size.width} ${size.height}`}
      preserveAspectRatio='none'
      className={`absolute inset-0 h-full w-full ${categoryTextClass('blue')} ${drawing && editable ? 'cursor-crosshair touch-none' : 'pointer-events-none'}`}
      onPointerDown={(event) => {
        if (drawing && lines.length < MAX_TRIPWIRES) {
          const p = point(event)
          begin(event, { id: null, endpoint: 1, points: [p, p] })
        }
      }}
      onPointerMove={(event) => {
        if (drag) {
          const points: Tripwire['points'] = [...drag.points]
          points[drag.endpoint] = point(event)
          setDrag({ ...drag, points })
        }
      }}
      onPointerCancel={() => setDrag(null)}
      onLostPointerCapture={() => setDrag(null)}
      onPointerUp={(event) => {
        if (!drag || !editable) {
          setDrag(null)
          return
        }
        const points: Tripwire['points'] = [...drag.points]
        points[drag.endpoint] = point(event)
        if (
          Math.hypot(
            points[1][0] - points[0][0],
            points[1][1] - points[0][1]
          ) >=
            MIN_LINE_LENGTH - 1e-9 &&
          JSON.stringify(points) !==
            JSON.stringify(
              drag.id ? lines.find((line) => line.id === drag.id)?.points : null
            )
        ) {
          onBeforeChange?.()
          onChange({
            ...draft,
            tripwires: drag.id
              ? lines.map((line) =>
                  line.id === drag.id ? { ...line, points } : line
                )
              : [
                  ...lines,
                  {
                    id: Array.from(
                      crypto.getRandomValues(new Uint32Array(4)),
                      (value) => value.toString(16).padStart(8, '0')
                    ).join(''),
                    name: t('region.tripwireDefaultName', {
                      number: lines.length + 1,
                    }),
                    points,
                    direction: 'both',
                  },
                ],
          })
        }
        setDrag(null)
      }}
    >
      <defs>
        <marker
          id={marker}
          viewBox='0 0 10 10'
          refX='8'
          refY='5'
          markerWidth='6'
          markerHeight='6'
          orient='auto-start-reverse'
        >
          <path d='M 0 0 L 10 5 L 0 10 z' fill='currentColor' />
        </marker>
      </defs>
      {lines.map((line) => (
        <TripwireGraphic
          key={line.id}
          line={drag?.id === line.id ? { ...line, points: drag.points } : line}
          {...size}
          marker={marker}
          editable={editable}
          drawing={drawing}
          onEndpoint={begin}
          onNudge={nudge}
        />
      ))}
      {drag && !drag.id && (
        <TripwireGraphic
          line={{
            id: 'preview',
            name: '',
            points: drag.points,
            direction: 'both',
          }}
          preview
          {...size}
          marker={marker}
          editable={false}
          drawing={drawing}
          onEndpoint={begin}
          onNudge={nudge}
        />
      )}
    </svg>
  )
}
