import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  // services/WebSocketManager.test.js is a manual browser utility, not a Vitest suite.
  test: { include: ['src/__tests__/**/*.test.{js,jsx}'] },
})
