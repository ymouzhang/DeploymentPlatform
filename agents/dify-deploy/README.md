# Dify 1.16.1 Agent 分支离线部署包

本模块把 `agents/dify` 子模块的 `feature/1.16.1` 分支打包成符合 DP 约定的 amd64 离线安装包。
浏览器、API 客户端和其他系统直接访问 Dify Nginx 的 `api_port`；独立健康容器只检查 Dify API、Web、
Local Sandbox 和 Agent Backend，并在 `port` 提供 DP 要求的 `GET /healthz`，不会代理业务请求。

部署包装放在父仓库而不是 Dify 子模块中。这样可以继续通过 `git submodule update --remote agents/dify`
跟踪指定分支，不会让子模块长期处于 dirty 状态。

## 与官方 Dify 1.16.1 的区别

当前 `feature/1.16.1` 是定制分支，不等同于官方 `langgenius/dify:1.16.1`：

- 增加 `agent_backend`、`local_sandbox` 和隔离的 `agent_ssrf_proxy`，用于 Agent V2 的运行、Shell 工作区
  和受控网络访问；
- 增加一次性的 `plugin_initializer`，给已有工作区补装内置插件；
- Worker 使用源码构建的 `ymouzhang/dify-api:1.16.1-bundled`，而不是官方 API 镜像；
- 内置 `langgenius/openai_api_compatible:0.0.64` 与 `langgenius/agent:0.0.47` 两个带完整 wheels 的
  离线插件，新工作区和已有工作区均不需要访问 Marketplace 或 PyPI；
- 当前内置插件依赖仅支持 CPython 3.12 / Linux x86_64，因此完整离线包只支持 amd64，不能因为官方
  核心镜像支持 arm64 就制作 arm64 安装包。

分支内的实现和制品约束见 `agents/dify/api/bundled_plugins/DESIGN.zh-CN.md`。

## 与官方 Docker Compose 部署的区别

官方 Compose 允许通过 profiles 自由组合 PostgreSQL/MySQL 和多种向量库。DP 首版固定为
PostgreSQL + Redis + Weaviate + OpenDAL 本地文件存储，并启用 Collaboration 与上述 Agent 服务。
固定组合避免用户在离线环境切换 profile 后才发现安装包缺少对应镜像。

DP 包还做了这些收敛：

- 只发布 `api_port` 和 DP 健康端口 `port`，不发布 PostgreSQL、Redis、Weaviate、插件调试端口和无效的
  HTTPS 占位端口；
- 禁用 Marketplace、版本检查、Weaviate telemetry 和 Certbot，运行时不主动访问这些互联网服务；
- 对所有数据库、Agent、Sandbox、Plugin Daemon 和 Weaviate 密钥做启动前校验，拒绝示例密钥；
- 默认允许 `10.0.0.0/8`、`172.16.0.0/12` 和 `192.168.0.0/16` 通过 Dify SSRF Proxy，以便 Dify
  调用本机或内网 LiteLLM。生产环境应把 `private_network_allowlist` 缩小到实际网关网段；
- 启动固定使用 `--pull never --no-build`；离线服务器既不会拉镜像，也不会尝试从源码构建 Worker；
- `stop.sh` 只停止容器，不删除 `volumes/` 下的数据库、Redis、Weaviate、文件和插件数据。

官方的 `docker-compose.middleware.yaml` 仅适合从源码运行 API/Web 的开发环境，本模块没有使用它。

## 服务与端口

- `nginx`：在宿主机 `api_port` 提供 Dify Web、Console API、Service API 和 WebSocket；
- `health`：在宿主机 `port` 提供唯一的 `/healthz`；
- API、Worker、PostgreSQL、Redis、Weaviate、Sandbox、Plugin Daemon 和 Agent 服务只在 Compose 网络中通信。

默认访问方式：

```bash
# DP 健康检查
curl http://<Dify服务器IP>:18500/healthz

# Dify Web 和 API
curl http://<Dify服务器IP>:30080/
```

DP 页面目前展示顶层 `port`，所以页面显示的是健康端口 18500；实际业务地址来自 `api_port` 和
`public_url`。DP 日志页面会按 Compose 服务名前缀混合展示整套 Dify 服务日志。

## 配置

`config/config.json` 是 DP 唯一管理的配置文件：

- `port`：DP 健康端口；
- `api_port`：Dify Nginx 业务端口；
- `public_url`：客户端实际使用的 `http://IP或域名:api_port`，端口必须与 `api_port` 一致；
- `images`：安装包内 13 张唯一镜像，离线部署后不能修改；
- `database` 和其他 `*_key`、`*_token`、`*_password`：必须替换全部 `change-me`；
- `server_workers`、`celery_workers`：API 与异步任务并发数；
- `private_network_allowlist`：Dify 可通过 SSRF Proxy 访问的私网 CIDR。

可使用以下方式分别生成密钥，再写入配置文件。已经产生数据后不可随意修改这些密钥：

```bash
openssl rand -base64 36 | tr -d '\n'
```

如果 LiteLLM 部署在同一台机器，Dify 中的 OpenAI Compatible Provider 应填写宿主机内网 IP 和
LiteLLM 的 `api_port`，不要填写 LiteLLM 的 DP 健康端口。容器中的 `127.0.0.1` 指向容器自身，不能
用来访问宿主机 LiteLLM。

## 制作离线包

先初始化子模块：

```bash
git submodule update --init agents/dify
cd agents/dify-deploy
./package.sh
```

打包机需要 Go、Python 3、Docker、Docker Compose v2、`gzip`、`tar` 和 `sha256sum`。脚本会：

1. 离线验证两个 bundled plugin 制品及其 SHA-256；
2. 从子模块源码构建 `ymouzhang/dify-api:1.16.1-bundled`；
3. 拉取本机缺少的其余镜像，并校验全部镜像都是 linux/amd64；
4. 生成镜像引用与 Image ID 清单；
5. 将 13 张镜像和运行脚本一起写入 `dp-dify-linux-amd64.tar.gz`。

可用选项：

```bash
# always、missing（默认）或 never
DIFY_PULL_IMAGES=missing ./package.sh

# 使用本机已构建的 bundled 镜像
DIFY_BUILD_BUNDLED_IMAGE=0 ./package.sh

# Dockerfile 构建网络；host 模式可使用打包机上的 localhost 代理
DIFY_BUILD_NETWORK=host ./package.sh

# 仅做结构和 Compose 校验，不生成可离线部署的镜像归档
DIFY_BUNDLE_IMAGES=0 DIFY_BUILD_BUNDLED_IMAGE=0 ./package.sh
```

`DIFY_OUTPUT_DIR` 可覆盖产物目录，默认仍为当前模块的 `dist/`；根目录统一打包脚本会用它把 Dify 包
直接写入项目根目录 `dist/`。

## 在 DP 中安装

1. 上传 `dist/dp-dify-linux-amd64.tar.gz`，服务类型建议填写 `dify`；
2. 修改 `config/config.json` 的 IP、端口和全部占位密钥；
3. 确认目标机 Docker 数据目录和安装目录空间足够；
4. 点击安装。启动脚本加载镜像、执行数据库迁移和插件初始化，并等待整体健康检查通过；
5. 通过 `http://<public_url>/install` 完成第一个管理员初始化（若 Dify 自动跳转则按页面提示操作）。

目标服务器只需要 Linux amd64、Docker Engine 和支持 Compose `!override`/`!reset` 的 Docker Compose
v2，不需要 Go、Python 或互联网。Python 健康程序运行在已经随包交付的 Dify API 镜像内。

## 数据备份与升级

必须同时备份安装目录中的 `volumes/`、`config/config.json` 和生成的 `.env`。重点数据包括：

- `volumes/db/data`：Dify 和 Plugin Daemon 数据库；
- `volumes/app/storage`：上传文件和本地对象存储；
- `volumes/weaviate`：知识库向量数据；
- `volumes/plugin_daemon`：插件运行环境与缓存；
- `volumes/redis/data`：队列和缓存状态。

升级子模块或镜像前必须先备份，并在联网验证环境完成数据库迁移、内置插件和 Agent 功能验证后重新制作
完整离线包。不能只替换 API/Web 镜像而保留旧版 Compose。
