import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import { writeFileSync } from 'fs'
import { resolve } from 'path'

/**
 * 构建后重新写入 dist/.gitkeep。
 *
 * Go 端用 go:embed 嵌入 web/dist（见 web/embed.go），目录不存在时 go build
 * 直接失败；仓库里因此提交了这个占位文件。vite 的 emptyOutDir 每次构建都会
 * 清空 dist，不写回的话 git 会显示它被删除。
 */
function keepEmbedPlaceholder(): Plugin {
  return {
    name: 'homehub-keep-embed-placeholder',
    apply: 'build',
    closeBundle() {
      writeFileSync(resolve(__dirname, 'dist/.gitkeep'), '')
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), keepEmbedPlaceholder()],

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
