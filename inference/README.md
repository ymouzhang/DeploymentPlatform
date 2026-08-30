# DP 推理服务安装包

`inference` 提供符合 DP 安装包约定的 vLLM 和 SGLang 服务。两个服务都使用 OpenAI 兼容 API。
推理 API 由 Docker 直接映射到引擎容器，LiteLLM 不经过任何 Python 代理；独立健康容器只补充
DP 要求的 `GET /healthz`，上游引擎健康时固定返回 `{"status":"ok"}`。

`configctl/` 是 vLLM 与 SGLang 共用的独立 Go 项目：`cmd/configctl` 负责命令行入口，
`internal/config` 负责读取、校验 DP 配置并生成 Compose 环境。打包后只携带静态编译的
`dp-inference-config` 二进制，目标服务器不需要 Go。

## 打包

打包机需要 Go、Docker、Docker Compose v2、`gzip`、`tar` 和 `sha256sum`。默认安装包会包含配置中
指定的完整推理镜像，可直接在无镜像仓库、无互联网的目标环境运行：

```bash
cd inference
./package.sh
```

默认生成当前 Go 架构的两个包。交叉打包可执行：

```bash
INFERENCE_ARCH=arm64 ./package.sh
```

也可以只生成其中一个引擎的安装包：

```bash
INFERENCE_ENGINES=vllm ./package.sh
INFERENCE_ENGINES=sglang ./package.sh
```

`INFERENCE_ENGINES` 也接受以空格或逗号分隔的 `vllm`、`sglang` 列表，默认打包两者。

镜像处理选项：

```bash
# 默认：仅在本机缺少镜像时拉取，然后导出离线镜像
INFERENCE_PULL_IMAGES=missing ./package.sh

# 始终重新拉取指定平台的固定标签后再导出
INFERENCE_PULL_IMAGES=always ./package.sh

# 仅供快速开发验证，产物不保证能在离线目标机运行
INFERENCE_BUNDLE_IMAGES=0 ./package.sh
```

`INFERENCE_GZIP_LEVEL` 可设置为 1–9，默认 6。两张推理镜像体积很大，打包需要足够的磁盘空间
和时间。交叉架构打包时，本地镜像平台必须与 `INFERENCE_ARCH` 一致；否则应使用
`INFERENCE_PULL_IMAGES=always` 拉取目标平台镜像。
`INFERENCE_OUTPUT_DIR` 可将产物写到指定目录，默认仍为 `inference/dist/`。

产物位于 `inference/dist/`：

```text
dp-vllm-linux-amd64.tar.gz
dp-vllm-linux-amd64.tar.gz.sha256
dp-sglang-linux-amd64.tar.gz
dp-sglang-linux-amd64.tar.gz.sha256
```

压缩包带单层公共根目录，DP 安装时会自动剥离。`start.sh`、`stop.sh` 均在包根目录且具有
执行权限；每个包只包含 `config/config.json` 这一种平台配置文件。默认包内还包含：

- `inference-image.tar.gz`：`docker image save` 导出的离线推理镜像；
- `offline-image.ref`：镜像引用；
- `offline-image.id`：打包时的不可变镜像 ID。

## 在 DP 中部署

1. 在“安装包管理”上传对应 `.tar.gz`，服务类型建议分别填写 `vllm`、`sglang`。
2. 创建环境并选择服务类型。vLLM 与 SGLang 部署到同一台机器时应使用不同安装目录和端口。
3. 在服务配置中至少修改 `model_path`、`served_model_name` 和 `api_key`；同机部署时所有实例的
   `port`、`api_port` 都不能重复。离线部署不能修改 `image`，否则会与包内镜像校验失败。
4. 点击安装。首次启动会先加载包内镜像，再创建容器；脚本不等待大模型加载，因此模型加载期间
   健康状态暂时不可用，就绪后自动变为运行中。

离线镜像会让安装包远超 DP 默认的 2GiB 上传限制。启动 DP 前，应根据实际包大小提高 `.env` 中
的限制和上传超时，例如：

```env
DP_UPLOAD_MAX_BYTES=32212254720
DP_UPLOAD_TIMEOUT=30m
```

目标服务器要求：

- Linux，CPU 架构与安装包一致；
- Docker Engine、Docker Compose v2、NVIDIA Container Toolkit；
- SSH 用户能够执行 Docker；
- `model_path` 是目标服务器上已存在的模型绝对目录；
- 安装目录和 Docker 数据目录有足够空间容纳压缩包、解压文件及加载后的镜像。

目标机不需要访问镜像仓库。`start.sh` 会先核对配置镜像与包内元数据：本机缺少对应镜像或镜像
ID 不同时执行 `docker image load`，已加载同一镜像时跳过重复导入；随后使用 Compose
`--pull never` 启动，确保不会意外联网。模型权重不包含在安装包内，仍需提前复制到目标机的
`model_path`。

Compose 将引擎 API 直接发布到 `0.0.0.0:<api_port>`，所以宿主机、LiteLLM 以及能路由到该
宿主机的内网机器均可访问。DP 健康接口独立发布到 `0.0.0.0:<port>`：

```bash
curl http://<目标服务器IP>:18000/healthz
curl -H 'Authorization: Bearer <api_key>' http://<目标服务器IP>:8000/v1/models
```

还需确保主机防火墙/安全组允许 DP 服务器访问 `port`，并允许 LiteLLM 访问 `api_port`。不要将
这两个端口直接暴露到公网；推理 API 的认证密钥会保存在 DP 的实例配置中，应按内部敏感配置管理。

## 配置说明

两个模板的顶层 `port` 是 DP 固定读取的健康检查端口；`api_port` 是 LiteLLM 直接访问推理引擎
的宿主机端口。容器内部引擎固定使用 8000，Docker 将 `api_port` 直接映射到引擎，不经过 Python。
独立健康服务监听 `port`，仅将 `/healthz` 探测转换到引擎的 `/health`，其他路径返回 404/405。

默认端口：

| 服务 | LiteLLM 直连 `api_port` | DP 健康 `port` |
| --- | ---: | ---: |
| vLLM | 8000 | 18000 |
| SGLang | 8001 | 18001 |

通用字段包括镜像、宿主机模型路径、逻辑模型名、API 密钥、上下文长度、显存比例、最大并发、
张量并行数和数据类型。`extra_args` 可为对应引擎追加原生命令行参数；它是高级逃生口，错误参数会
使引擎容器反复重启。修改已安装实例的配置后，需要在 DP 中再次点击“启动”以重建容器并生效。

`stop.sh` 使用 `docker compose down` 停止并删除服务容器和项目网络，但保留 Hugging Face、
SGLang 编译缓存等命名卷。DP 的“重置”同样不会删除这些缓存卷。
