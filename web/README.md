# voxbox-web

voxbox 前端（Task 11 骨架）：Vite + React 19 + TypeScript + Tailwind CSS v4。

```bash
npm install
npm run dev    # http://localhost:5173，/api 代理到 http://localhost:8081
npm run build  # tsc -b && vite build
```

- `src/lib/api.ts`：REST 封装，解析后端统一包络 `{code,data,message}`（code!==0 抛 message）。
- `src/lib/ws.ts`：`useTaskEvents()` 订阅 `/api/ws` 任务事件，断线 2s 自动重连。
- `src/theme.css`：CSS variables 双主题，`:root` 暗色默认、`:root.light` 亮色，顶栏按钮切换并持久化到 localStorage。
