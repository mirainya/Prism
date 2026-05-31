import path from 'path';
import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig(({ mode }) => {
    const env = loadEnv(mode, '.', '');
    return {
      server: {
        port: 3001,
        host: '0.0.0.0',
        proxy: {
          '/api': {
            target: 'http://localhost:23523',
            changeOrigin: true,
          },
        },
      },
      plugins: [tailwindcss(), react()],
      define: {
        'process.env.API_KEY': JSON.stringify(env.GEMINI_API_KEY),
        'process.env.GEMINI_API_KEY': JSON.stringify(env.GEMINI_API_KEY)
      },
      resolve: {
        alias: {
          '@': path.resolve(__dirname, '.'),
        }
      },
      build: {
        // 仅预加载首屏真正需要的依赖,剔除重型懒加载库(codemirror/recharts/dndkit),
        // 否则它们会被 modulepreload 强制塞进首屏,拖慢白屏时间
        modulePreload: {
          resolveDependencies: (_filename, deps) => {
            return deps.filter((dep) =>
              !/(codemirror|recharts|dndkit)-[^/]*\.js$/.test(dep)
            );
          },
        },
        rollupOptions: {
          output: {
            manualChunks: {
              // React 核心 + 路由,各页面共享,单独成 vendor chunk 长期缓存
              'react-vendor': ['react', 'react-dom', 'react-router-dom'],
              // 图表库,仅 Dashboard 使用,体积大,独立懒加载
              recharts: ['recharts'],
              // 拖拽库,仅能力配置/对话模型排序使用
              dndkit: ['@dnd-kit/core', '@dnd-kit/sortable', '@dnd-kit/utilities'],
              // 代码编辑器,仅 JSON 编辑场景使用
              codemirror: ['@uiw/react-codemirror', '@codemirror/lang-json'],
            }
          }
        }
      }
    };
});
