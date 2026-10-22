import { type VehicleItem } from '../types/ai-result'

export interface DrawVehicleOverlayOptions {
  ctx: CanvasRenderingContext2D
  width: number
  height: number
  vehicles: VehicleItem[]
  unrecognizedLabel?: string
  clear?: boolean
}

interface PlateColorStyle {
  strokeColor: string
  fillColor: string
  badgeBg: string
  textColor: string
}

function resolvePlateStyle(plateColor: string): PlateColorStyle {
  const c = plateColor.trim().toLowerCase()
  if (c.includes('黄') || c.includes('yellow')) {
    return {
      strokeColor: '#eab308',
      fillColor: 'rgba(234, 179, 8, 0.12)',
      badgeBg: 'rgba(202, 138, 4, 0.92)',
      textColor: '#ffffff',
    }
  }
  if (c.includes('绿') || c.includes('green') || c.includes('新能源')) {
    return {
      strokeColor: '#10b981',
      fillColor: 'rgba(16, 185, 129, 0.12)',
      badgeBg: 'rgba(5, 150, 105, 0.92)',
      textColor: '#ffffff',
    }
  }
  if (c.includes('白') || c.includes('white')) {
    return {
      strokeColor: '#e2e8f0',
      fillColor: 'rgba(241, 245, 249, 0.15)',
      badgeBg: 'rgba(51, 65, 85, 0.92)',
      textColor: '#f8fafc',
    }
  }
  if (c.includes('黑') || c.includes('black')) {
    return {
      strokeColor: '#475569',
      fillColor: 'rgba(30, 41, 59, 0.20)',
      badgeBg: 'rgba(15, 23, 42, 0.95)',
      textColor: '#f8fafc',
    }
  }
  // 默认蓝色车牌 (Blue)
  return {
    strokeColor: '#3b82f6',
    fillColor: 'rgba(59, 130, 246, 0.12)',
    badgeBg: 'rgba(29, 78, 216, 0.92)',
    textColor: '#ffffff',
  }
}

/**
 * 判断机动车外框与车牌框是否属于「纯车牌退化模式」
 * 若车牌框与机动车框几乎重合，则不绘制外层车身框，避免重叠干扰。
 */
function isDegradedVehicleMode(
  vehicleBox: [number, number, number, number],
  plateBox: [number, number, number, number]
): boolean {
  const eps = 0.02
  return (
    Math.abs(vehicleBox[0] - plateBox[0]) < eps &&
    Math.abs(vehicleBox[1] - plateBox[1]) < eps &&
    Math.abs(vehicleBox[2] - plateBox[2]) < eps &&
    Math.abs(vehicleBox[3] - plateBox[3]) < eps
  )
}

/**
 * 在 Canvas 上绘制低延迟、高保真的机动车与车牌检测/识别叠加层 (VehicleOverlayRenderer)
 * 采用归一化坐标自适应映射，支持颜色分类自适应着色、四角 HUD 科技视觉增强与退化模式判定。
 */
export function drawVehicleOverlay({
  ctx,
  width,
  height,
  vehicles,
  unrecognizedLabel = '未识别车牌',
  clear = true,
}: DrawVehicleOverlayOptions): void {
  if (clear) {
    ctx.clearRect(0, 0, width, height)
  }

  if (!vehicles || vehicles.length === 0 || width <= 0 || height <= 0) {
    return
  }

  const baseLineWidth = Math.max(2, Math.round(width / 640))
  const cornerLength = Math.max(8, Math.round(width / 100))
  const fontSize = Math.max(11, Math.min(18, Math.round(width / 80)))
  const smallFontSize = Math.max(10, Math.min(14, Math.round(width / 100)))

  for (const vehicle of vehicles) {
    const [rawVx1, rawVy1, rawVx2, rawVy2] = vehicle.vehicle_box
    const vx1 = Math.max(0, Math.min(1, rawVx1))
    const vy1 = Math.max(0, Math.min(1, rawVy1))
    const vx2 = Math.max(vx1, Math.min(1, rawVx2))
    const vy2 = Math.max(vy1, Math.min(1, rawVy2))

    const vbx = Math.round(vx1 * width)
    const vby = Math.round(vy1 * height)
    const vbw = Math.round((vx2 - vx1) * width)
    const vbh = Math.round((vy2 - vy1) * height)

    // 1. 判断是否需要绘制外层车身框（非退化模式且尺寸合法时绘制）
    const hasPlates = vehicle.plates && vehicle.plates.length > 0
    const isDegraded =
      hasPlates &&
      vehicle.plates.length === 1 &&
      isDegradedVehicleMode(vehicle.vehicle_box, vehicle.plates[0].box)

    if (!isDegraded && vbw > 0 && vbh > 0) {
      ctx.save()

      // 车身框采用较为轻量的中性灰蓝 HUD 虚线与浅色底
      ctx.fillStyle = 'rgba(148, 163, 184, 0.05)'
      ctx.fillRect(vbx, vby, vbw, vbh)

      ctx.strokeStyle = 'rgba(148, 163, 184, 0.7)'
      ctx.lineWidth = Math.max(1, baseLineWidth - 1)
      ctx.setLineDash([5, 5])
      ctx.strokeRect(vbx, vby, vbw, vbh)

      // 车身标签（例如车型、颜色、Track ID）
      const vehicleDetails: string[] = []
      if (vehicle.vehicle_type) vehicleDetails.push(vehicle.vehicle_type)
      if (vehicle.vehicle_color) vehicleDetails.push(vehicle.vehicle_color)
      if (vehicle.track_id > 0) vehicleDetails.push(`#${vehicle.track_id}`)

      if (vehicleDetails.length > 0) {
        ctx.setLineDash([])
        ctx.font = `${smallFontSize}px system-ui, -apple-system, sans-serif`
        ctx.textBaseline = 'middle'

        const vText = vehicleDetails.join(' · ')
        const vMetrics = ctx.measureText(vText)
        const vBadgePad = 4
        const vBadgeHeight = smallFontSize + 6
        const vBadgeWidth = vMetrics.width + vBadgePad * 2

        let vBadgeX = vbx
        if (vBadgeX + vBadgeWidth > width) {
          vBadgeX = Math.max(0, width - vBadgeWidth - 2)
        }
        let vBadgeY = vby - vBadgeHeight - 2
        if (vBadgeY < 0) {
          vBadgeY = vby + 2
        }

        ctx.fillStyle = 'rgba(30, 41, 59, 0.85)'
        if (typeof ctx.roundRect === 'function') {
          ctx.beginPath()
          ctx.roundRect(vBadgeX, vBadgeY, vBadgeWidth, vBadgeHeight, 2)
          ctx.fill()
        } else {
          ctx.fillRect(vBadgeX, vBadgeY, vBadgeWidth, vBadgeHeight)
        }

        ctx.fillStyle = '#e2e8f0'
        ctx.fillText(vText, vBadgeX + vBadgePad, vBadgeY + vBadgeHeight / 2)
      }

      ctx.restore()
    }

    // 2. 绘制挂载的车牌框与车牌标识
    if (hasPlates) {
      for (const plate of vehicle.plates) {
        const [rawPx1, rawPy1, rawPx2, rawPy2] = plate.box
        const px1 = Math.max(0, Math.min(1, rawPx1))
        const py1 = Math.max(0, Math.min(1, rawPy1))
        const px2 = Math.max(px1, Math.min(1, rawPx2))
        const py2 = Math.max(py1, Math.min(1, rawPy2))

        const pbx = Math.round(px1 * width)
        const pby = Math.round(py1 * height)
        const pbw = Math.round((px2 - px1) * width)
        const pbh = Math.round((py2 - py1) * height)

        if (pbw <= 0 || pbh <= 0) continue

        ctx.save()

        const style = resolvePlateStyle(plate.plate_color)

        // 2.1 矩形背景与边框
        ctx.fillStyle = style.fillColor
        ctx.fillRect(pbx, pby, pbw, pbh)

        ctx.strokeStyle = style.strokeColor
        ctx.lineWidth = baseLineWidth
        ctx.setLineDash([])
        ctx.strokeRect(pbx, pby, pbw, pbh)

        // 2.2 四角 HUD 强化视觉（仅在尺寸足够时绘制）
        if (pbw > cornerLength * 2 && pbh > cornerLength * 2) {
          ctx.lineWidth = baseLineWidth + 1.5
          ctx.strokeStyle = style.strokeColor
          const cl = Math.min(cornerLength, Math.floor(Math.min(pbw, pbh) / 3))

          // 左上
          ctx.beginPath()
          ctx.moveTo(pbx, pby + cl)
          ctx.lineTo(pbx, pby)
          ctx.lineTo(pbx + cl, pby)
          ctx.stroke()

          // 右上
          ctx.beginPath()
          ctx.moveTo(pbx + pbw - cl, pby)
          ctx.lineTo(pbx + pbw, pby)
          ctx.lineTo(pbx + pbw, pby + cl)
          ctx.stroke()

          // 左下
          ctx.beginPath()
          ctx.moveTo(pbx, pby + pbh - cl)
          ctx.lineTo(pbx, pby + pbh)
          ctx.lineTo(pbx + cl, pby + pbh)
          ctx.stroke()

          // 右下
          ctx.beginPath()
          ctx.moveTo(pbx + pbw - cl, pby + pbh)
          ctx.lineTo(pbx + pbw, pby + pbh)
          ctx.lineTo(pbx + pbw, pby + pbh - cl)
          ctx.stroke()
        }

        // 2.3 车牌号码标牌 (Badge)
        ctx.font = `bold ${fontSize}px system-ui, -apple-system, sans-serif`
        ctx.textBaseline = 'middle'

        const scorePercent = Math.round(
          Math.max(0, Math.min(1, plate.score)) * 100
        )
        const plateText = plate.plate_number.trim()
        const labelText = plateText
          ? `${plateText} (${scorePercent}%)`
          : `${unrecognizedLabel} (${scorePercent}%)`

        const metrics = ctx.measureText(labelText)
        const badgePaddingX = 6
        const badgeHeight = fontSize + 8
        const badgeWidth = metrics.width + badgePaddingX * 2

        let badgeX = pbx
        if (badgeX + badgeWidth > width) {
          badgeX = Math.max(0, width - badgeWidth - 2)
        }

        let badgeY = pby - badgeHeight - 2
        if (badgeY < 0) {
          badgeY = pby + 2
        }

        ctx.fillStyle = style.badgeBg
        if (typeof ctx.roundRect === 'function') {
          ctx.beginPath()
          ctx.roundRect(badgeX, badgeY, badgeWidth, badgeHeight, 3)
          ctx.fill()
        } else {
          ctx.fillRect(badgeX, badgeY, badgeWidth, badgeHeight)
        }

        ctx.fillStyle = style.textColor
        ctx.fillText(
          labelText,
          badgeX + badgePaddingX,
          badgeY + badgeHeight / 2
        )

        ctx.restore()
      }
    }
  }
}
