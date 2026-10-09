import { createFileRoute, redirect } from '@tanstack/react-router'
import { AuthenticatedLayout } from '@/components/layout/authenticated-layout'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated')({
  beforeLoad: async ({ location }) => {
    const { auth } = useAuthStore.getState()
    if (!auth.user) {
      // 尝试向后端校验当前 Session Cookie 并恢复用户信息
      const user = await auth.checkAuth()
      if (!user) {
        // 如果访问的是控制台首页，直接跳转 /sign-in，不在地址栏附加冗余的 ?redirect=%2F
        const isRoot = location.pathname === '/' || location.pathname === ''
        throw redirect({
          to: '/sign-in',
          search: isRoot
            ? undefined
            : {
                redirect: location.pathname,
              },
        })
      }
    }
  },
  component: AuthenticatedLayout,
})
