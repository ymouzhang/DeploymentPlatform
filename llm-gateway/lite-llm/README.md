# LiteLLM 离线部署包

本模块将 LiteLLM Proxy 和 PostgreSQL 打包为符合 DP 约定的离线安装包。业务系统和 Agents 直接访问
LiteLLM 的 OpenAI 兼容 API；独立健康容器只把 LiteLLM `/health/readiness` 转换为 DP 要求的
`GET /healthz`，不会代理任何推理请求。

`configctl/` 是独立 Go 项目：`cmd/configctl` 只负责命令行入口，`internal/config` 负责读取、校验
DP 配置并生成 Compose 环境与 LiteLLM 运行配置。打包后只携带静态编译的 `dp-litellm-config`
二进制，目标服务器不需要 Go。

## 服务结构

- `litellm`：对宿主机 `api_port` 发布网关 API 和 `/ui`；
- `db`：保存模型、虚拟密钥和用量记录，仅在 Compose 内部网络开放；
- `health`：对宿主机 `port` 发布唯一的 `/healthz` 接口。

PostgreSQL 使用命名卷持久化。执行 `stop.sh` 或 DP 的停止、重置操作不会删除数据库卷。

## 制作离线包

打包机需要 Go、Docker、Docker Compose v2、`gzip`、`tar` 和 `sha256sum`。默认拉取本机缺少的
LiteLLM 与 PostgreSQL 镜像，并将两张镜像一起写入安装包：

```bash
cd llm-gateway/lite-llm
./package.sh
```

默认生成当前 Go 架构的包。交叉架构打包可执行：

```bash
LITELLM_ARCH=arm64 LITELLM_PULL_IMAGES=always ./package.sh
```

可用打包选项：

```bash
# missing（默认）、always 或 never
LITELLM_PULL_IMAGES=missing ./package.sh

# 仅用于快速开发校验；生成的包不保证能在离线目标机启动
LITELLM_BUNDLE_IMAGES=0 ./package.sh

# 1 到 9，默认 6
LITELLM_GZIP_LEVEL=6 ./package.sh
```

产物位于 `dist/`：

```text
dp-litellm-linux-amd64.tar.gz
dp-litellm-linux-amd64.tar.gz.sha256
```

包内的 `offline-images.tar.gz` 是 `docker image save` 导出的 LiteLLM 与 PostgreSQL 镜像；
`offline-images.manifest` 记录镜像引用和打包时的不可变 Image ID。启动脚本只在本机镜像缺失或 ID
不匹配时加载归档，随后使用 Compose `--pull never`，不会访问镜像仓库。

## 在 DP 中部署

1. 在安装包管理中上传产物，服务类型建议填写 `litellm`。
2. 创建环境，确保安装目录和 Docker 数据目录有足够空间。
3. 部署前修改服务配置中的所有 `change-me` 占位值。启动脚本会拒绝使用占位密钥。
4. 按实际推理服务修改 `models`。同机访问 inference 模块时可保留
   `host.docker.internal`，并将端口改为对应引擎的 `api_port`。
5. 点击安装。脚本会加载离线镜像，等待 PostgreSQL、LiteLLM 和健康适配器全部就绪后返回。

目标服务器只需要 Linux、Docker Engine、Docker Compose v2，不需要 Go、Python或互联网，也不需要
GPU。安装包包含完整运行镜像，但不包含推理模型；LiteLLM 通过内网访问已经部署的 vLLM/SGLang。

默认访问地址：

```bash
# DP 健康检查
curl http://<网关服务器IP>:18400/healthz

# LiteLLM 模型列表和管理界面
curl -H 'Authorization: Bearer <master_key>' http://<网关服务器IP>:4000/v1/models
# 浏览器访问 http://<网关服务器IP>:4000/ui
```

防火墙应允许业务系统访问 `api_port`，并允许 DP 服务器访问 `port`，不应开放 PostgreSQL 端口。
DP 页面当前展示顶层 `port`，因此显示的是健康端口 18400；实际网关端口在配置的 `api_port` 中。
DP 日志页面会混合显示 `litellm`、`db` 和 `health` 三个 Compose 服务的日志，并带服务名前缀。

## 配置字段

`config/config.json` 是 DP 唯一管理的配置文件：

- `port`：DP `/healthz` 端口；
- `api_port`：业务系统直接访问 LiteLLM 的端口；
- `litellm_image`、`postgres_image`：必须与离线包清单一致，离线部署后不能修改；
- `master_key`：LiteLLM 管理员密钥和 UI 密码，必须以 `sk-` 开头；
- `salt_key`：数据库敏感字段加密密钥；写入模型凭据后不得修改，否则旧凭据无法解密；
- `database`：内部 PostgreSQL 库名、用户名和密码；已有数据卷投入使用后不要修改；
- `store_model_in_db`：允许从 Admin UI 动态维护模型；
- `num_workers`：LiteLLM worker 数量。单机可按 CPU 和内存调整；每增加 worker 都会增加数据库连接和内存；
- `models`：启动时加载的静态模型列表。

每个模型包含对外的 `model_name`、LiteLLM provider 模型名 `model`、推理引擎 `api_base` 和上游
`api_key`。vLLM/SGLang 的 OpenAI 兼容接口可使用：

```json
{
  "model_name": "Qwen3-4B-Instruct-2507",
  "model": "openai/Qwen3-4B-Instruct-2507",
  "api_base": "http://host.docker.internal:8000/v1",
  "api_key": "推理引擎配置的 api_key"
}
```

如果推理服务位于另一台机器，将 `api_base` 改为
`http://<推理服务器内网IP>:<api_port>/v1`。不要填写 inference 模块的健康端口。

## 数据与升级

必须备份 PostgreSQL 命名卷以及当前实例配置中的 `master_key`、`salt_key` 和数据库密码。更新安装包
时必须保持配置文件路径仍为 `config/config.json`。版本升级应先备份数据库，并在联网环境验证新版本
LiteLLM 对现有数据库迁移和配置格式的兼容性，再制作新的离线包。
