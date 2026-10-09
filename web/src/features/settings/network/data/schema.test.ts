import { describe, expect, it } from 'vitest'
import {
  interfaceFormSchema,
  isValidIPv4,
  isValidSubnetMask,
  parseDnsString,
  pingFormSchema,
} from './schema'

describe('network schema validation', () => {
  describe('isValidIPv4', () => {
    it('accepts valid IPv4 addresses', () => {
      expect(isValidIPv4('192.168.1.1')).toBe(true)
      expect(isValidIPv4('10.0.0.1')).toBe(true)
      expect(isValidIPv4('172.16.254.1')).toBe(true)
      expect(isValidIPv4('8.8.8.8')).toBe(true)
      expect(isValidIPv4('255.255.255.255')).toBe(true)
      expect(isValidIPv4('0.0.0.0')).toBe(true)
    })

    it('rejects invalid IPv4 addresses', () => {
      expect(isValidIPv4('')).toBe(false)
      expect(isValidIPv4('192.168.1.256')).toBe(false)
      expect(isValidIPv4('192.168.1')).toBe(false)
      expect(isValidIPv4('192.168.1.1.1')).toBe(false)
      expect(isValidIPv4('abc.def.ghi.jkl')).toBe(false)
    })
  })

  describe('isValidSubnetMask', () => {
    it('accepts valid continuous subnet masks', () => {
      expect(isValidSubnetMask('255.255.255.0')).toBe(true)
      expect(isValidSubnetMask('255.255.0.0')).toBe(true)
      expect(isValidSubnetMask('255.0.0.0')).toBe(true)
      expect(isValidSubnetMask('255.255.255.128')).toBe(true)
      expect(isValidSubnetMask('255.255.255.252')).toBe(true)
    })

    it('rejects discontinuous or invalid subnet masks', () => {
      expect(isValidSubnetMask('255.255.255.1')).toBe(false)
      expect(isValidSubnetMask('255.0.255.0')).toBe(false)
      expect(isValidSubnetMask('255.255.255.256')).toBe(false)
      expect(isValidSubnetMask('0.0.0.0')).toBe(false)
      expect(isValidSubnetMask('invalid')).toBe(false)
    })
  })

  describe('parseDnsString', () => {
    it('splits comma and space separated DNS addresses', () => {
      expect(parseDnsString('8.8.8.8, 1.1.1.1')).toEqual(['8.8.8.8', '1.1.1.1'])
      expect(parseDnsString('8.8.8.8 114.114.114.114')).toEqual([
        '8.8.8.8',
        '114.114.114.114',
      ])
      expect(parseDnsString('')).toEqual([])
    })
  })

  describe('interfaceFormSchema', () => {
    it('accepts valid DHCP config', () => {
      const result = interfaceFormSchema.safeParse({
        mode: 'dhcp',
        setDefault: true,
      })
      expect(result.success).toBe(true)
    })

    it('accepts valid static config', () => {
      const result = interfaceFormSchema.safeParse({
        mode: 'static',
        ipAddress: '192.168.1.100',
        subnetMask: '255.255.255.0',
        gateway: '192.168.1.1',
        dns: '8.8.8.8, 1.1.1.1',
        setDefault: false,
      })
      expect(result.success).toBe(true)
    })

    it('rejects static config without ipAddress', () => {
      const result = interfaceFormSchema.safeParse({
        mode: 'static',
        subnetMask: '255.255.255.0',
        setDefault: false,
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0].path).toContain('ipAddress')
      }
    })

    it('rejects static config with invalid subnetMask', () => {
      const result = interfaceFormSchema.safeParse({
        mode: 'static',
        ipAddress: '192.168.1.100',
        subnetMask: '255.255.255.1',
        setDefault: false,
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0].path).toContain('subnetMask')
      }
    })

    it('rejects invalid DNS format', () => {
      const result = interfaceFormSchema.safeParse({
        mode: 'dhcp',
        dns: '8.8.8.8, invalid-dns',
        setDefault: false,
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0].path).toContain('dns')
      }
    })
  })

  describe('pingFormSchema', () => {
    it('accepts target', () => {
      expect(pingFormSchema.safeParse({ target: '192.168.1.1' }).success).toBe(
        true
      )
      expect(pingFormSchema.safeParse({ target: 'example.com' }).success).toBe(
        true
      )
      expect(pingFormSchema.safeParse({ target: '' }).success).toBe(false)
    })
  })
})
