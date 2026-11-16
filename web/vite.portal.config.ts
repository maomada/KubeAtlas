import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
      '@maomada/k8s-ui': path.resolve(import.meta.dirname, '../packages/k8s-ui/src'),
      'monaco-editor/esm/vs/editor/editor.worker.js': 'monaco-editor/editor/editor.worker.js',
    },
  },
  plugins: [
    tailwindcss(),
    react(),
    {
      name: 'portal-embed-marker',
      generateBundle() {
        this.emitFile({ type: 'asset', fileName: '.gitkeep', source: '' })
      },
    },
  ],
  build: {
    outDir: '../internal/portalui/dist',
    emptyOutDir: true,
    rolldownOptions: { input: path.resolve(import.meta.dirname, 'portal.html') },
  },
})
