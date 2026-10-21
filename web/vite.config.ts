import path from 'path'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import { playwright } from '@vitest/browser-playwright'
import { defineConfig, configDefaults } from 'vitest/config'

// https://vite.dev/config/
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
    include: ['@radix-ui/react-avatar'],
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8000',
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
    unstubEnvs: true,
    setupFiles: ['./src/test-utils/setup.ts'],
    browser: {
      enabled: true,
      provider: playwright(),
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
