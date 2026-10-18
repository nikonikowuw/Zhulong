import {
  LayoutDashboard,
  Video,
  Monitor,
  Palette,
  Settings,
  Network,
  Clock,
  ScrollText,
  UserCheck,
  HardDrive,
  Cpu,
  Images,
  Workflow,
} from 'lucide-react'
import { ZhulongLogo } from '@/assets/zhulong-logo'
import { type SidebarData } from '../types'

export const sidebarData: SidebarData = {
  user: {
    name: 'Administrator',
    email: 'admin@zhulong.local',
    avatar: '',
  },
  teams: [
    {
      name: 'Zhulong Edge',
      logo: ZhulongLogo,
      plan: '边缘智能宿主',
    },
  ],
  navGroups: [
    {
      title: 'General',
      items: [
        {
          title: 'Dashboard',
          url: '/',
          icon: LayoutDashboard,
        },
        {
          title: 'Cameras',
          url: '/cameras',
          icon: Video,
        },
        {
          title: 'Persons',
          url: '/persons',
          icon: UserCheck,
        },
        {
          title: 'Algorithms',
          url: '/algorithms',
          icon: Cpu,
        },
        {
          title: 'Tasks',
          url: '/tasks',
          icon: Workflow,
        },
        {
          title: 'Live View',
          url: '/live',
          icon: Monitor,
        },
        {
          title: 'Records',
          url: '/records',
          icon: Images,
        },
        {
          title: 'Audit Logs',
          url: '/audit',
          icon: ScrollText,
        },
      ],
    },
    {
      title: 'Other',
      items: [
        {
          title: 'Settings',
          icon: Settings,
          items: [
            {
              title: 'Appearance',
              url: '/settings/appearance',
              icon: Palette,
            },
            {
              title: 'Network',
              url: '/settings/network',
              icon: Network,
            },
            {
              title: 'Time',
              url: '/settings/time',
              icon: Clock,
            },
            {
              title: 'Storage',
              url: '/settings/storage',
              icon: HardDrive,
            },
          ],
        },
      ],
    },
  ],
}
