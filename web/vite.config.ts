import { defineConfig } from 'vitest/config';
import preact from '@preact/preset-vite';

// Assets are content-hashed and served immutably by the Go binary; index.html
// stays unhashed and is revalidated on every load.
export default defineConfig({
  plugins: [preact()],
  build: {
    outDir: '../internal/api/dist',
    // The directory is committed (empty) so `go build` can always resolve the
    // //go:embed pattern. Vite must not delete it; `make ui` removes the
    // previous build output instead.
    emptyOutDir: false,
    target: 'es2022',
    assetsDir: 'assets',
    sourcemap: false,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name].[hash].js',
        chunkFileNames: 'assets/[name].[hash].js',
        assetFileNames: 'assets/[name].[hash][extname]',
      },
    },
  },
  server: {
    proxy: { '/api': 'http://127.0.0.1:8080' },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
