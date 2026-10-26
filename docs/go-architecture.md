# Go 架构阅读入口

当前前后端架构、职责和扩展方式统一见 [Plinth 架构](architecture.md)。

架构入口已按 2026-10-06 主线 `3da956cd9a88bf16f29e9790cb750dd8eb0f3572` 更新。
本批保留 Go 单进程与统一任务内核，补充资产检查、原子写入、付费步骤恢复、会话保存和同源解码资源的实际结构。
后续架构变化须在同一交付更新架构总览与受影响的边界文档，执行根 AGENTS.md 的同步规则。

- [Provider / Schema 边界](provider-schema-architecture.md)：公开任务、私有子作业和 Provider 接入要求。
- 历史文件 `docs/agent-workspace-implementation-ledger.md`（已移出仓库，见内部 Git 历史 `b43d4018`）：源码能力、测试证据及未闭环项。
- [本地与部署后验收](../dev_test.md)、[运行手册](../README-go.md)、[运维协议](../dev_ops.md)。

本页原先混合了 Next→Go 迁移分析、设计提案及若干时点的实现状态，完整内容已移出仓库，只保留在内部 Git 历史中。历史示例不能替代现行冻结输入、独立同意和公开投影合同。
