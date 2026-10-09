import { describe, expect, it } from 'vitest'
import { loginSchema, initAdminSchema } from './schema'

describe('Auth Schemas', () => {
  describe('loginSchema', () => {
    it('应成功解析有效登录凭据', () => {
      const result = loginSchema.safeParse({
        username: 'admin',
        password: 'password123',
        rememberMe: true,
      })
      expect(result.success).toBe(true)
      if (result.success) {
        expect(result.data.username).toBe('admin')
        expect(result.data.rememberMe).toBe(true)
      }
    })

    it('当用户名为空时应校验失败', () => {
      const result = loginSchema.safeParse({
        username: '   ',
        password: 'password123',
        rememberMe: true,
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0]?.message).toBe('请输入用户名')
      }
    })

    it('当密码为空时应校验失败', () => {
      const result = loginSchema.safeParse({
        username: 'admin',
        password: '',
        rememberMe: true,
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0]?.message).toBe('请输入密码')
      }
    })
  })

  describe('initAdminSchema', () => {
    it('应成功解析合法的管理员初始化数据', () => {
      const result = initAdminSchema.safeParse({
        username: 'admin',
        password: 'Password123!',
        confirmPassword: 'Password123!',
      })
      expect(result.success).toBe(true)
    })

    it('当用户名少于 3 个字符时应报错', () => {
      const result = initAdminSchema.safeParse({
        username: 'ab',
        password: 'Password123!',
        confirmPassword: 'Password123!',
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0]?.message).toBe('用户名至少需要 3 个字符')
      }
    })

    it('当密码少于 8 个字符时应报错', () => {
      const result = initAdminSchema.safeParse({
        username: 'admin',
        password: '1234567',
        confirmPassword: '1234567',
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0]?.message).toBe(
          '密码长度至少需要 8 个字符'
        )
      }
    })

    it('当两次输入密码不一致时应报错', () => {
      const result = initAdminSchema.safeParse({
        username: 'admin',
        password: 'Password123!',
        confirmPassword: 'DifferentPassword123!',
      })
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0]?.message).toBe('两次输入的密码不一致')
        expect(result.error.issues[0]?.path).toEqual(['confirmPassword'])
      }
    })
  })
})
