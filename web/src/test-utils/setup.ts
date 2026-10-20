// ⚠️ 必须加载真实样式表。
//
// 此前只有 main.tsx 引入样式，组件测试跑在**无 Tailwind** 的环境里，导致：
//   1. 对样式元素做 getComputedStyle / getBoundingClientRect 断言，拿到的是
//      UA 默认值（如 button 的 `padding: 1px 6px`），量出来的布局毫无意义；
//   2. __screenshots__ 下产出的截图是**无样式渲染**，视觉回归形同虚设；
//   3. 想校验状态色只能退化成断言 class 字符串（见 algorithm-status-badge.test.tsx）。
// 引入后这三类断言与截图才真正反映用户所见。
import '@/styles/index.css'
import { beforeEach } from 'vitest'
import i18n from '@/lib/i18n'

beforeEach(async () => {
  await i18n.changeLanguage('zh-Hans')
})
