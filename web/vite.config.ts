import path from 'path'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import { playwright } from '@vitest/browser-playwright'
import { appendFileSync } from 'node:fs'
import { defineConfig, configDefaults } from 'vitest/config'

// https://vite.dev/config/
// 开发/测试用的后端地址。默认沿用仓库约定的 8000；同步实测这类需要独立后端实例的
// 场景用 `ZHULONG_API_TARGET` 指到别处，避免和人在跑的 `make run` 抢同一个端口。
const apiTarget = process.env.ZHULONG_API_TARGET ?? 'http://127.0.0.1:8000'

export default defineConfig({
  plugins: [
    tanstackRouter({
      target: 'react',
      autoCodeSplitting: true,
    }),
    react(),
    tailwindcss(),
  ],
  optimizeDeps: {
    include: [
      '@radix-ui/react-avatar',
      '@radix-ui/react-tabs',
      '@radix-ui/react-slider',
      'fflate',
    ],
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
        ws: true,
        configure: (proxy) => {
          proxy.on('error', () => {
            // 静默忽略本地开发/测试期间后端未启动时的连接异常
          })
        },
      },
    },
  },
  test: {
    silent: 'passed-only',
    // 并发上限 4（默认取 CPU 数）。每个 worker 都是一个真实 Chromium 上下文，8 个并发在
    // 8 核机上跑约 150 个文件时，会以 3~4 个随机的「Failed to fetch dynamically imported
    // module / Cannot connect to the iframe」收场 —— 失败文件每次不同，全是 iframe 握手
    // 超时，不是用例问题。4 个并发连续两轮全绿（151 passed | 1 skipped）。
    maxWorkers: 4,
    // 测量类用例（sync-measure）的结论走 console.info，而 `silent: 'passed-only'`
    // 会把**通过**用例的控制台输出吞掉。设了 ZHULONG_TEST_CONSOLE_LOG 时把浏览器侧
    // 日志逐行追加到文件，测量结果才能入档，而不用为了「看到一行数」故意让用例失败。
    onConsoleLog(log: string) {
      const file = process.env.ZHULONG_TEST_CONSOLE_LOG
      if (!file) return
      appendFileSync(file, `${log}\n`)
    },
    unstubEnvs: true,
    setupFiles: ['./src/test-utils/setup.ts'],
    browser: {
      enabled: true,
      // 同步实测需要一个真的在走的 AudioContext：无手势策略下它会永远 suspended，
      // currentTime 不推进，音频时间轴就退化成一条假的。其余用例不碰音频输出，无副作用。
      provider: playwright({
        launchOptions: {
          args: ['--autoplay-policy=no-user-gesture-required'],
        },
      }),
      instances: [
        {
          browser: 'chromium',
          name: 'chromium-default',
          exclude: [
            ...configDefaults.exclude,
            'src/components/config-drawer.test.tsx',
            'src/features/tasks/components/region-editor-dialog.test.tsx',
          ],
        },
        {
          browser: 'chromium',
          name: 'chromium-desktop',
          include: [
            'src/components/config-drawer.test.tsx',
            'src/features/tasks/components/region-editor-dialog.test.tsx',
          ],
          viewport: { width: 1280, height: 896 },
        },
      ],
      api: { host: '127.0.0.1' },
    },
    coverage: {
      // include: ['src/**/*.{js,jsx,ts,tsx}'], // Uncomment to expand the report to all src/**/* so untested modules appear as 0% coverage.
      exclude: [
        'src/components/ui/**',
        'src/assets/**',
        'src/tanstack-table.d.ts',
        'src/routeTree.gen.ts',
        'src/test-utils/**',
        'src/routes/**',
      ],
    },
  },
})
