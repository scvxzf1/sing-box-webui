# Web frontend

React、TypeScript 和 Vite 前端。开发服务器固定监听 `127.0.0.1:33333`，并将
`/api` 与 `/healthz` 代理到默认 Go API `127.0.0.1:33334`。

推荐从仓库根目录运行 `./scripts/dev.sh`。单独开发前端时使用：

```bash
npm install
npm run dev
```

质量检查：

```bash
npm run generate:api
npm run lint
npm run typecheck
npm test
npm run build
```
