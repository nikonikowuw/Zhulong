import React from 'react'
import {
  MINUTES_PER_DAY,
  timeStringToMinutes,
  type TimeRange,
} from '../data/schedule-config'

export interface ScheduleTimelineBarProps {
  ranges: TimeRange[]
}

/** 紧凑平滑的分钟级时间轴条 */
export function ScheduleTimelineBar({
  ranges,
}: ScheduleTimelineBarProps): React.JSX.Element {
  return (
    <div className='relative h-2 w-full overflow-hidden rounded-full bg-muted/60'>
      {/* 4 等分参考刻度点 (06:00, 12:00, 18:00) */}
      <div className='pointer-events-none absolute inset-0 flex justify-between px-[25%]'>
        <div className='h-full w-[1px] bg-background/50' />
        <div className='h-full w-[1px] bg-background/50' />
      </div>

      {/* 连续生效时段高亮 (精确到分钟) */}
      {ranges.map((r, idx) => {
        const startMin = timeStringToMinutes(r.start)
        const endMin = timeStringToMinutes(r.end)
        if (startMin >= endMin) return null
        const left = (startMin / MINUTES_PER_DAY) * 100
        const width = ((endMin - startMin) / MINUTES_PER_DAY) * 100
        return (
          <div
            key={idx}
            className='absolute top-0 bottom-0 rounded-full bg-primary transition-all duration-150'
            style={{
              left: `${left}%`,
              width: `${Math.max(1, width)}%`,
            }}
          />
        )
      })}
    </div>
  )
}
