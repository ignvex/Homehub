import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { resolve } from 'path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],

  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom'],
          charts: ['chart.js', 'react-chartjs-2'],
        },
      },
    },
  },

  // 开发服务器：代理 API 和静态资源到 Go 后端
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/static': 'http://localhost:8080',
    },
  },

  // 路径别名
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
})
