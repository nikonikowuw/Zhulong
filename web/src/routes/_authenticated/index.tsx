import { createFileRoute, redirect } from '@tanstack/react-router'

// 控制台首页没有独立的仪表盘页面：本机是边缘运维宿主，日常入口是实时监看。
// 保留 '/' 路由做重定向，是因为侧边栏 Logo 与已收藏的地址都指向根路径。
export const Route = createFileRoute('/_authenticated/')({
  beforeLoad: () => {
    throw redirect({
      to: '/live',
      replace: true,
    })
  },
})
