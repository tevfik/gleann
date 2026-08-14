import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('lucide-react')) return 'ui-vendor'
            if (id.includes('force-graph') || id.includes('d3-')) return 'graph-vendor'
            if (id.includes('react-markdown') || id.includes('remark-') || id.includes('micromark')) return 'md-vendor'
            return 'vendor'
          }
        }
      }
    }
  }
})
