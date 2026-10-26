import { describe, expect, it } from 'vitest'
import {
  diffGb28181Config,
  isValidDeviceID,
  mediaCapacity,
  parseSipListen,
  type Gb28181Config,
} from './schema'

const baseConfig: Gb28181Config = {
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

describe('gb28181 schema helpers', () => {
  describe('mediaCapacity', () => {
    it('divides the port span into RTP/RTCP pairs', () => {
      // span 101 → 50 complete pairs, same as the backend integer division
      expect(mediaCapacity(30000, 30100)).toBe(50)
      expect(mediaCapacity(30000, 30001)).toBe(1)
      expect(mediaCapacity(30000, 30000)).toBe(0)
    })

    it('returns zero for missing or inverted ranges', () => {
      expect(mediaCapacity(undefined, 30100)).toBe(0)
      expect(mediaCapacity(30100, 30000)).toBe(0)
    })
  })

  describe('parseSipListen', () => {
    it('splits host and port', () => {
      expect(parseSipListen('0.0.0.0:5060')).toEqual({
        host: '0.0.0.0',
        port: 5060,
      })
      expect(parseSipListen(' 192.168.1.10:5061 ')).toEqual({
        host: '192.168.1.10',
        port: 5061,
      })
    })

    it('rejects malformed values', () => {
      expect(parseSipListen('5060')).toBeNull()
      expect(parseSipListen(':5060')).toBeNull()
      expect(parseSipListen('0.0.0.0:')).toBeNull()
      expect(parseSipListen('0.0.0.0:abc')).toBeNull()
      expect(parseSipListen('')).toBeNull()
    })
  })

  describe('isValidDeviceID', () => {
    it('accepts exactly 20 digits', () => {
      expect(isValidDeviceID('34020000001320000001')).toBe(true)
      expect(isValidDeviceID('3402000000132000000')).toBe(false)
      expect(isValidDeviceID('340200000013200000011')).toBe(false)
      expect(isValidDeviceID('3402000000132000000a')).toBe(false)
    })
  })

  describe('diffGb28181Config', () => {
    it('returns nothing when desired equals startup', () => {
      expect(diffGb28181Config(baseConfig, { ...baseConfig })).toEqual([])
    })

    it('reports every changed field with both values', () => {
      const desired = {
        ...baseConfig,
        sipListen: '0.0.0.0:5061',
        rtpPortMax: 30200,
      }

      const differences = diffGb28181Config(desired, baseConfig)

      expect(differences).toEqual([
        {
          field: 'sipListen',
          desired: '0.0.0.0:5061',
          startup: '0.0.0.0:5060',
        },
        { field: 'rtpPortMax', desired: 30200, startup: 30100 },
      ])
    })
  })
})
