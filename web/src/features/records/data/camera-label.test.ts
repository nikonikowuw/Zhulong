import { describe, expect, it } from 'vitest'
import { cameraLabel, type CameraNameLookup } from './camera-label'

describe('cameraLabel', () => {
  const lookup: CameraNameLookup = (cameraId) =>
    cameraId === 'cam-gate' ? '南门枪机' : undefined

  it('resolves the camera id to its configured name', () => {
    expect(cameraLabel('cam-gate', lookup)).toBe('南门枪机')
  })

  it('falls back to the raw id when the camera is not in the list', () => {
    // 相机被删除时记录会 CASCADE 一起消失，但列表可能还没加载完 /
    // 或该相机不在当前缓存里。此时必须显示 ID，不能渲染空白。
    expect(cameraLabel('cam-unknown', lookup)).toBe('cam-unknown')
  })

  it('falls back to the raw id when no resolver is provided', () => {
    expect(cameraLabel('cam-gate')).toBe('cam-gate')
    expect(cameraLabel('cam-gate', undefined)).toBe('cam-gate')
  })

  it('falls back to the raw id when the camera name is blank', () => {
    const blankLookup: CameraNameLookup = () => '   '
    expect(cameraLabel('cam-gate', blankLookup)).toBe('cam-gate')
  })

  it('trims surrounding whitespace from the resolved name', () => {
    const paddedLookup: CameraNameLookup = () => '  南门枪机  '
    expect(cameraLabel('cam-gate', paddedLookup)).toBe('南门枪机')
  })
})
