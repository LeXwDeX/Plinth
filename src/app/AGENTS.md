# src/app — React 页面协作指引

> 本子树暂无独有约束，本文件只做真源指针索引、不复述事实（防止副本漂移）。发现子树独有的新坑再补记于此。

## 改动前必读（按改动类型）

- **页面 / 组件**：根 `AGENTS.md`「真源和分层」与 `docs/product.md`「工作区交互与验收边界」；Base UI 交互验证见 `dev_test.md` 第 10 节。
- **API 交互**：前端调用 Go HTTP API；后端实现约束见 `internal/AGENTS.md`，分层见 `docs/go-architecture.md`。
- **验证**：`dev_test.md` §2 是门禁唯一真源。
