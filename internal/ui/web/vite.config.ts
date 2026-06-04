import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import AutoImport from 'unplugin-auto-import/vite';
import Components from 'unplugin-vue-components/vite';
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers';

// Dev server proxy target: where a real running launcher serves /api/*.
// Override with: VITE_API_PROXY=http://127.0.0.1:54321 npm run dev
const apiProxy = process.env.VITE_API_PROXY || 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [
    vue(),
    AutoImport({ resolvers: [ElementPlusResolver()] }),
    Components({ resolvers: [ElementPlusResolver()] }),
  ],
  build: {
    // Go side: //go:embed assets/dist/* in internal/ui/server.go
    outDir: '../assets/dist',
    emptyOutDir: true,
    target: 'es2020',
  },
  server: {
    port: 5173,
    proxy: {
      '/api': apiProxy,
    },
  },
});
