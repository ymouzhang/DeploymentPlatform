# Deployment Platform

这是一个面向离线环境的 AI 服务部署仓库。DP 负责安装和管理服务，LiteLLM 负责统一模型入口，
vLLM/SGLang 负责模型推理，Dify 负责构建 AI 应用和 Agent。

```text
用户或应用 -> LiteLLM -> vLLM / SGLang
Dify -------> LiteLLM

DP：安装、启动、停止和查看以上服务
```

## 模块

| 模块 | 目录 | 用途 | 主要访问方式 |
| --- | --- | --- | --- |
| DP | [`dp/`](dp/) | Web 部署平台；管理安装包、服务器、配置、启停、健康状态和日志 | 浏览器访问 DP Web |
| vLLM | [`inference/vllm/`](inference/vllm/) | GPU 大模型推理服务，提供 OpenAI 兼容 API | LiteLLM 访问其 `api_port` |
| SGLang | [`inference/sglang/`](inference/sglang/) | 另一种 GPU 大模型推理服务，提供 OpenAI 兼容 API | LiteLLM 访问其 `api_port` |
| LiteLLM | [`llm-gateway/lite-llm/`](llm-gateway/lite-llm/) | 统一模型网关；把请求转发到 vLLM、SGLang 等推理服务 | 客户端和 Dify 访问其 `api_port` |
| Dify | [`agents/dify-deploy/`](agents/dify-deploy/) | AI 应用与 Agent 平台；使用定制 Dify 分支并支持离线部署 | 浏览器和 API 访问其 `api_port` |

`agents/dify/` 是 Dify 源码 submodule；`agents/dify-deploy/` 是适配 DP 的配置、健康检查和离线打包
脚本。不要直接把部署脚本写入 submodule。

所有由 DP 安装的服务都遵循同一端口约定：

- `port`：仅供 DP 调用 `/healthz`；
- `api_port`：业务端口，供浏览器、LiteLLM、Dify 或其他客户端访问。

## 快速使用

### 1. 制作离线包

在项目根目录执行：

```bash
# 打包全部模块
./package.sh --version v1.0.0

# 只打包一个模块
./package.sh --module vllm
./package.sh --module sglang
./package.sh --module litellm
./package.sh --module dify
./package.sh --module dp --version v1.0.0

# 同时打包多个模块
./package.sh --module litellm --module dify
```

可选模块：`all`、`dp`、`inference`、`vllm`、`sglang`、`litellm`、`dify`。`inference` 表示同时
打包 vLLM 和 SGLang。

Dify 使用 submodule。选择 `dify` 或 `all` 时，脚本会在缺失时自动执行初始化；也可以提前运行：

```bash
git submodule update --init --recursive agents/dify
```

### 2. 获取打包结果

每个模块生成独立压缩包和 `.sha256` 校验文件，统一放在根目录 `dist/`，不会生成总压缩包：

```text
dist/dp-v1.0.0-linux-amd64.tar.gz
dist/dp-vllm-linux-amd64.tar.gz
dist/dp-sglang-linux-amd64.tar.gz
dist/dp-litellm-linux-amd64.tar.gz
dist/dp-dify-linux-amd64.tar.gz
```

### 3. 部署 DP

把 DP 压缩包复制到管理服务器，解压后启动：

```bash
tar -xzf dp-v1.0.0-linux-amd64.tar.gz
cd dp-v1.0.0-linux-amd64
./dp.sh start
```

启动方法、账号配置和数据备份见 [`dp/README.md`](dp/README.md)。

### 4. 通过 DP 安装其他模块

1. 在 DP 的“安装包管理”中上传对应模块的 `.tar.gz`；
2. 添加目标服务器并创建环境；
3. 修改服务配置，特别是端口、密钥、模型路径和对接地址；
4. 点击安装；之后可在 DP 中启动、停止、检查健康状态和查看日志。

推荐顺序：先安装 vLLM/SGLang，再安装 LiteLLM 并配置推理服务地址，最后安装 Dify 并把模型供应商
地址配置为 LiteLLM 的 `api_port`。容器访问同机服务时应使用宿主机内网 IP 或
`host.docker.internal`，不能使用 `127.0.0.1`。

## 打包要求

- 打包机需要 Docker、Docker Compose v2、Go、Python 3、`tar`、`gzip` 和 `sha256sum`；
- 默认包包含 Docker 离线镜像，因此需要足够的磁盘空间和打包时间；
- Dify bundled 插件目前只支持 amd64；包含 Dify 时只能打包 amd64；
- 其他模块可用 `PACKAGE_ARCH=arm64` 单独打包；
- 可用 `PACKAGE_OUTPUT_DIR` 修改统一输出目录。

各模块的配置字段、镜像选项和部署细节以对应目录中的 README 为准。
