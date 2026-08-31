# DP

DP（Deployment Platform，部署管理平台）是面向内部环境的 Web 管理平台，用于按账号和服务类型管理安装包、目标服务器与服务配置，通过 SSH/SFTP 完成远程部署和生命周期操作，并通过目标服务的 HTTP `/healthz` 接口持续观测运行状态。

安装包配置支持 `config/config.json`、`config/config.yaml`、`config/application.yml` 和 `config/application.yaml`，每个安装包必须且只能使用其中一种。
安装包可以带一个公共顶层目录（例如 `dist/`）；配置端口支持顶层 `port` 或 `server.port`。
安装包内配置作为模板；每台服务器上的服务实例拥有独立配置，安装时写入对应远端目录。
上传安装包时可选择已有服务类型或创建自定义类型；每次上传形成不可变版本，可查看引用、切换当前版本并按保留策略清理安全的旧版本。
服务实例配置在保存前展示差异，每次保存和回滚都形成可追溯的不可变修订。
系统按账号隔离资源；管理员可通过管理总览、操作中心、账号资源交接、风险通知和审计日志治理全部账号的数据与运行状态，普通账号只能查看和操作自己的资源。
模型管理支持选择已有环境并复用 SSH 凭据，将超大 `.tar.gz` 按分片直接中转到目标机，支持断点续传、
安全校验解压、任务日志和带归属标记校验的删除；DP 数据盘不保存完整模型副本。

需求与设计：

- [产品需求](docs/prd.md)
- [架构设计](docs/architecture.md)
- [模型管理需求与设计](docs/model-management.md)
- [管理员产品优化路线图](docs/admin-product-optimization.md)
- [OpenAPI 契约](api/openapi.yaml)

### 文档维护约定

所有新增功能和会改变用户行为的修改必须先更新相关文档，再修改代码；实现完成后还需校准文档与实际行为，并将文档和代码放在同一提交中。管理员优化项还必须在同一提交中更新[管理员产品优化路线图](docs/admin-product-optimization.md)的状态。路线图全部完成并将有效规则沉淀到正式文档后，应删除路线图及其引用；本条文档先行约定继续长期有效。

## 本地启动

要求：

- Go 1.26+
- Node.js 24+
- Corepack

安装前端依赖：

```bash
cd web
corepack pnpm install
```

生成开发主密钥：

```bash
openssl rand -base64 32
U49iorlwGTL+1Yob4mtDp7GBC7MuEN/9BZruRV5URzM=
```

启动后端：

```bash
export DP_MASTER_KEY="<上一步生成的值>"
export DP_ADMIN_USERNAME="admin"
export DP_ADMIN_PASSWORD="<至少 8 位的初始管理员密码>"
make dev-backend
```

另开终端启动前端开发服务器：

```bash
make dev-frontend
```

浏览器访问 `http://127.0.0.1:5173`。

## 生产构建

```bash
make build
```

前端会构建到 `webui/dist` 并嵌入 Go 程序，最终产物为 `bin/dp`。

运行：

```bash
export DP_MASTER_KEY="<Base64 编码的 32 字节主密钥>"
export DP_ADMIN_USERNAME="admin"
export DP_ADMIN_PASSWORD="<至少 8 位的初始管理员密码>"
export DP_DATA_DIR="./data"
./bin/dp
```

默认监听 `127.0.0.1:8080`。生产环境应通过内网/VPN 访问，并在反向代理层启用 HTTPS。

## Docker Compose 部署

### 当前源码目录一键启动

要求已安装 Docker Engine 和 Docker Compose v2，执行：

```bash
./scripts/dp.sh start
```

脚本首次运行会创建 `.env`、生成独立的 32 字节主密钥和随机初始管理员密码、创建 `data/`，随后通过
Docker Compose 构建并启动服务。访问 `http://<服务器IP>:30199`。
从无账号版本升级且已有 `.env` 时，脚本会保留原配置并自动追加、打印一次随机初始管理员密码；首次登录后应立即修改密码。

常用命令：

```bash
./scripts/dp.sh status
./scripts/dp.sh logs
./scripts/dp.sh restart
./scripts/dp.sh stop
./scripts/dp.sh down
```

### 一键编译并生成离线部署包

```bash
DP_PLATFORM=linux/arm64 ./scripts/build-package.sh arm64-v1.0.0
./scripts/build-package.sh v1.0.0
```

构建过程会在 Docker 中安装依赖、执行前后端测试、编译程序、构建运行镜像，并生成：

在跨架构构建中，测试和前端构建使用构建主机的原生架构执行，Go 程序再交叉编译为 `DP_PLATFORM` 指定的目标架构；因此在 x86_64 主机生成 ARM64 包时不依赖 QEMU 执行 Vitest，最终镜像及其中的 `dp` 二进制仍为 ARM64。

```text
dist/dp-v1.0.0-linux-amd64.tar.gz
dist/dp-v1.0.0-linux-amd64.tar.gz.sha256
```

将压缩包复制到目标 Linux 服务器后：

```bash
tar -xzf dp-v1.0.0-linux-amd64.tar.gz
cd dp-v1.0.0-linux-amd64
./dp.sh start
```

目标服务器只需 Docker 与 Docker Compose，无需 Go、Node.js，也不依赖外网下载镜像。

### 持久化与备份

Compose 使用以下宿主机文件：

- `./data:/app/data`：SQLite 数据库、上传的安装包和操作日志；
- `.env`：主密钥及运行参数，由 Compose 读取，不写入镜像。

必须同时备份 `data/` 和 `.env`。主密钥丢失或改变后，数据库中已有的 SSH 密码将无法
解密。容器重建、升级或迁移时应保留这两项。

## 常用命令

```bash
make test
make lint
make build
make docker-up
make package
```

完整环境变量见 [.env.example](.env.example)。

版本与治理相关参数：`DP_PACKAGE_VERSION_RETENTION` 控制每个服务类型的版本保留目标数量，`DP_OPERATION_RETENTION_DAYS` 控制终态操作及 JSONL 日志保留期，`DP_NOTIFICATION_RETENTION_DAYS` 控制已处理通知保留期，`DP_STALE_ACCOUNT_DAYS` 控制长期未登录账号提醒阈值（默认 90 天）。被环境引用的安装包版本、未处理通知和运行中操作不会被自动清理。
