import { z } from 'zod'
import { type TFunction } from 'i18next'

export function createLoginSchema(t: TFunction) {
  return z.object({
    username: z
      .string()
      .trim()
      .min(1, t('validation.usernameRequired', { ns: 'auth' })),
    password: z
      .string()
      .min(1, t('validation.passwordRequired', { ns: 'auth' })),
    rememberMe: z.boolean(),
  })
}

export type LoginFormValues = z.infer<ReturnType<typeof createLoginSchema>>

export function createInitAdminSchema(t: TFunction) {
  return z
    .object({
      username: z
        .string()
        .trim()
        .min(3, t('validation.usernameMin', { ns: 'auth' }))
        .max(32, t('validation.usernameMax', { ns: 'auth' })),
      password: z
        .string()
        .min(8, t('validation.passwordMin', { ns: 'auth' }))
        .max(64, t('validation.passwordMax', { ns: 'auth' })),
      confirmPassword: z
        .string()
        .min(1, t('validation.confirmPasswordRequired', { ns: 'auth' })),
    })
    .refine((data) => data.password === data.confirmPassword, {
      message: t('validation.passwordMismatch', { ns: 'auth' }),
      path: ['confirmPassword'],
    })
}

export type InitAdminFormValues = z.infer<
  ReturnType<typeof createInitAdminSchema>
>
