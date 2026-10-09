import { z } from 'zod'

export const loginSchema = z.object({
  username: z.string().trim().min(1, '请输入用户名'),
  password: z.string().min(1, '请输入密码'),
  rememberMe: z.boolean(),
})

export type LoginFormValues = z.infer<typeof loginSchema>

export const initAdminSchema = z
  .object({
    username: z
      .string()
      .trim()
      .min(3, '用户名至少需要 3 个字符')
      .max(32, '用户名不能超过 32 个字符'),
    password: z
      .string()
      .min(8, '密码长度至少需要 8 个字符')
      .max(64, '密码长度不能超过 64 个字符'),
    confirmPassword: z.string().min(1, '请再次输入初始密码'),
  })
  .refine((data) => data.password === data.confirmPassword, {
    message: '两次输入的密码不一致',
    path: ['confirmPassword'],
  })

export type InitAdminFormValues = z.infer<typeof initAdminSchema>
