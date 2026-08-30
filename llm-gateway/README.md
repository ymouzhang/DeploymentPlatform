# LLM 网关

`llm-gateway` 存放统一访问推理服务的网关模块。目前提供：

- [`lite-llm`](./lite-llm/)：LiteLLM Proxy、PostgreSQL 和 DP 健康适配服务，支持制作完整离线安装包。

网关的业务 API 直接由 LiteLLM 暴露；健康适配服务只满足 DP 的 `/healthz` 检查，不转发模型请求。
