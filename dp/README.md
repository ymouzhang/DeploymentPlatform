# DP

DP（Deployment Platform，部署管理平台）是面向内网服务器的 Web 管理平台，用于管理主机、安装包、服务实例和模型，通过 SSH/SFTP 完成远程部署与生命周期操作，并通过目标服务的 HTTP `/healthz` 接口持续观测运行状态。

核心资源采用明确的两层模型：

- **主机**：一台 SSH 目标机及其连接凭据，同一台主机只注册一次；
- **服务实例**：某个服务类型在某台主机上的一次部署；同一主机可以创建多个不同或同类服务实例。

使用顺序：上传服务安装包 → 注册并校验主机 → 在服务管理中选择主机和安装包创建实例 → 安装、配置、启动和查看日志。模型直接关联主机，不依赖服务实例。

安装包配置支持 `config/config.json`、`config/config.yaml`、`config/application.yml` 和 `config/application.yaml`，每个安装包必须且只能使用其中一种。
安装包可以带一个公共顶层目录（例如 `dist/`）；配置端口支持顶层 `port` 或 `server.port`。
安装包内配置作为模板；每台服务器上的服务实例拥有独立配置，安装时写入对应远端目录。
上传安装包时可选择已有服务类型或创建自定义类型；每次上传形成不可变版本，可查看引用、切换当前版本并按保留策略清理安全的旧版本。
服务实例配置在保存前展示差异，每次保存和回滚都形成可追溯的不可变修订。
系统通过 Core RBAC 管理用户、角色、权限和 `own/all` 数据范围；授权默认拒绝并由后端统一执行。具有全局权限的角色可以治理全部账号资源，受限角色只能访问被授予范围内的数据。
模型管理支持选择已有主机并复用 SSH 凭据，将超大 `.tar.gz` 按分片直接中转到目标机，支持断点续传、
安全校验解压、任务日志和带归属标记校验的删除；DP 数据盘不保存完整模型副本。

需求与设计：

- [产品需求](docs/prd.md)
- [架构设计](docs/architecture.md)
- [模型管理需求与设计](docs/model-management.md)
- [RBAC 与 PostgreSQL 重构设计](docs/rbac-postgresql-refactor.md)
- [OpenAPI 契约](api/openapi.yaml)

### 文档维护约定

所有新增功能和会改变用户行为的修改必须同步更新相关文档；实现完成后需校准文档、OpenAPI 契约与实际行为，并将文档和代码放在同一提交中。

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
export DP_DATABASE_URL="postgres://dp:<密码>@127.0.0.1:5432/dp?sslmode=disable"
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

Compose 默认将 DP 发布到宿主机 `0.0.0.0:30199`，可通过 `DP_HTTP_PORT` 修改宿主机端口。生产环境应通过内网/VPN 控制访问范围，并在反向代理层启用 HTTPS。

## Docker Compose 部署

### 当前源码目录一键启动

要求已安装 Docker Engine 和 Docker Compose v2，执行：

```bash
./scripts/dp.sh start
```

脚本首次运行会创建 `.env`、生成独立的 32 字节主密钥和随机初始管理员密码、创建 `data/`，随后通过
Docker Compose 构建并启动服务。访问 `http://<服务器IP>:30199`。
本版本采用全新 PostgreSQL 数据结构，不提供旧资源模型或 SQLite 数据的兼容迁移；已有 `.env` 缺少 PostgreSQL 凭据时脚本会拒绝启动，需按
`.env.example` 创建新配置。首次登录后应立即修改随机初始管理员密码。

常用命令：

```bash
./scripts/dp.sh status
./scripts/dp.sh logs
./scripts/dp.sh restart
./scripts/dp.sh stop
./scripts/dp.sh down
./scripts/dp.sh backup
./scripts/dp.sh restore backups/<备份目录>
```

### 一键编译并生成离线部署包

```bash
DP_PLATFORM=linux/arm64 ./scripts/build-package.sh arm64-v1.0.0
./scripts/build-package.sh v1.0.0
```

构建过程会在 Docker 中安装依赖、执行前后端测试、编译程序、构建运行镜像，并生成：

如通过 `DP_POSTGRES_IMAGE` 指定内部镜像仓库或定制 PostgreSQL 镜像，打包脚本会将该镜像及其完整名称同步写入离线包的 `.env.example`；DP 与 PostgreSQL 镜像均按 `DP_PLATFORM` 生成或拉取。

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

### 持久化与备份（RBAC/PostgreSQL 重构目标）

Compose 使用以下持久化数据：

- `./data:/app/data`：上传的安装包、操作日志和任务文件；
- PostgreSQL 独立持久卷：账号、RBAC 和全部业务元数据；
- `.env`：主密钥、PostgreSQL 凭据及运行参数，由 Compose 读取，不写入镜像。

必须同时备份 `data/`、`.env` 和 `pg_dump` 产物。主密钥丢失或改变后，数据库中已有的 SSH 密码将无法
解密。容器重建、升级或迁移时应保留这三项。本轮不兼容重构不导入旧 SQLite 数据库。

## 常用命令

```bash
make test
make lint
make build
make docker-up
make package
```

完整环境变量见 [.env.example](.env.example)。

版本与治理相关参数：`DP_PACKAGE_VERSION_RETENTION` 控制每个服务类型的版本保留目标数量，`DP_OPERATION_RETENTION_DAYS` 控制终态操作及 JSONL 日志保留期，`DP_NOTIFICATION_RETENTION_DAYS` 控制已处理通知保留期，`DP_STALE_ACCOUNT_DAYS` 控制长期未登录账号提醒阈值（默认 90 天）。被服务实例引用的安装包版本、未处理通知和运行中操作不会被自动清理。
