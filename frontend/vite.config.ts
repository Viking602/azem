import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": new URL("./src", import.meta.url).pathname },
  },
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  build: {
    outDir: "dist",
    emptyOutDir: false,
    // Desktop app ships all assets locally; still split heavy vendors for cache + load.
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            {
              name: "react-vendor",
              test: /node_modules[\\/](react|react-dom|scheduler)([\\/]|$)/,
            },
            {
              name: "markdown",
              test: /node_modules[\\/](react-markdown|remark-|mdast-|micromark|unified|unist-|hast-|property-information|vfile|decode-named-character|character-entities|devlop|comma-separated-tokens|space-separated-tokens|trim-lines|ccount|escape-string-regexp|markdown-table|zwitch|longest-streak|mdurl|micromark-|decode-named)/,
            },
            {
              name: "icons",
              test: /node_modules[\\/]lucide-react([\\/]|$)/,
            },
            {
              name: "motion",
              test: /node_modules[\\/](motion|framer-motion)([\\/]|$)/,
            },
            {
              name: "xterm",
              test: /node_modules[\\/]@xterm[\\/]/,
            },
          ],
        },
      },
    },
  },
});
