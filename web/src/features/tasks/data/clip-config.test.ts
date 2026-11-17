import { describe, expect, it } from 'vitest'
import {
  DEFAULT_CLIP_CONFIG,
  parseClipConfig,
  serializeClipConfig,
  supportsClipRecording,
  supportsRecognitionFilter,
} from './clip-config'

describe('clip-config', () => {
  it.each(['', '{}', '   ', 'not json', '[]', 'null', undefined, null])(
    'treats %j as the disabled default',
    (raw) => {
      expect(parseClipConfig(raw)).toEqual(DEFAULT_CLIP_CONFIG)
    }
  )

  it('round-trips a full config', () => {
    const json = serializeClipConfig({
      enabled: true,
      preSeconds: 3,
      postSeconds: 20,
      recognitionFilter: 'stranger',
    })
    expect(JSON.parse(json)).toEqual({
      enabled: true,
      preSeconds: 3,
      postSeconds: 20,
      recognitionFilter: 'stranger',
    })
    expect(parseClipConfig(json)).toEqual({
      enabled: true,
      preSeconds: 3,
      postSeconds: 20,
      recognitionFilter: 'stranger',
    })
  })

  it('clamps out-of-range seconds instead of dropping the whole config', () => {
    expect(
      parseClipConfig(
        '{"enabled":true,"preSeconds":99,"postSeconds":0,"recognitionFilter":"bogus"}'
      )
    ).toEqual({
      enabled: true,
      preSeconds: 15,
      postSeconds: 1,
      recognitionFilter: 'all',
    })
  })

  it('fills missing fields with defaults', () => {
    expect(parseClipConfig('{"enabled":true}')).toEqual({
      ...DEFAULT_CLIP_CONFIG,
      enabled: true,
    })
  })

  it('clamps and rounds values on serialize', () => {
    expect(
      JSON.parse(
        serializeClipConfig({
          enabled: true,
          preSeconds: 0.4,
          postSeconds: 61.7,
          recognitionFilter: 'known',
        })
      )
    ).toEqual({
      enabled: true,
      preSeconds: 1,
      postSeconds: 60,
      recognitionFilter: 'known',
    })
  })

  it('only offers the recognition filter for person_structure algorithms', () => {
    expect(supportsRecognitionFilter('person_structure')).toBe(true)
    expect(supportsRecognitionFilter('detection')).toBe(false)
    expect(supportsRecognitionFilter('vehicle_structure')).toBe(false)
    expect(supportsRecognitionFilter(undefined)).toBe(false)
  })

  it('normalizes disabled config to {} and hides clip recording on vehicle_structure', () => {
    expect(
      serializeClipConfig({
        enabled: false,
        preSeconds: 10,
        postSeconds: 20,
        recognitionFilter: 'stranger',
      })
    ).toBe('{}')
    expect(supportsClipRecording('detection')).toBe(true)
    expect(supportsClipRecording('classification')).toBe(true)
    expect(supportsClipRecording('person_structure')).toBe(true)
    expect(supportsClipRecording('vehicle_structure')).toBe(false)
    expect(supportsClipRecording(undefined)).toBe(false)
  })
})
