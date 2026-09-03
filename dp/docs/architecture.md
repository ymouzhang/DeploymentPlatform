# DP 架构设计

> 状态：已实现
> 更新时间：2026-09-02
> 本次为不兼容重构，不读取或迁移旧资源模型的数据。

## 1. 设计目标

DP 是一个模块化单体：React 前端与 Go API 编译为一个程序，PostgreSQL 保存元数据，远端操作统一通过 SSH/SFTP 执行。设计重点是：一台主机只维护一份连接凭据，同一主机可以承载任意多个服务实例。

## 2. 资源模型

```text
账号
 ├─ 安装包（服务类型与不可变版本）
 ├─ 主机（SSH 地址、凭据、指纹、架构）
 │   ├─ 服务实例（服务类型、安装目录、配置、状态、标签）
 │   └─ 模型（目标目录、上传与部署任务）
 └─ 审计、操作、通讯
```

### 主机 `Host`

主机仅描述 SSH 端点，不包含服务类型、安装目录或安装状态。唯一业务键为 `(owner_id, ip, ssh_port)`。修改 IP、端口、SSH 用户或密码后，清空既有指纹、架构和校验时间，要求重新校验。

主机仍被服务实例或模型引用时禁止删除。主机导入导出只处理连接信息，密码保持加密；导入文件必须使用相同主密钥。

### 服务实例 `ServiceInstance`

服务实例表示一个服务包在一台主机上的一次部署，包含 `host_id`、`service_type`、`install_dir`、实例名称、配置和安装状态。同一主机的安装目录必须唯一，防止两个实例覆盖彼此文件。

创建实例必须选择同账号的既有主机和已上传安装包。已安装实例不能更换主机、服务类型或安装目录；需要先执行重置。删除服务实例不会删除主机。

### 模型 `Model`

模型直接关联 `host_id`。上传、解压和删除复用主机凭据，与 vLLM、SGLang 等具体服务实例没有从属关系。

## 3. 模块职责

```text
cmd/dp                          依赖装配与进程生命周期
internal/domain                 领域对象、输入校验、公共错误
internal/application/hosts.go  主机用例与依赖规则
internal/application/service_instances.go
                                服务实例用例与主机/安装包归属校验
internal/model                  模型上传、部署、删除任务编排
internal/operation              服务安装、启动、停止、重置编排
internal/health                 服务健康状态采集
internal/remote                 SSH/SFTP 与远端脚本执行
internal/repository/postgres    按资源拆分的数据访问
internal/httpapi                HTTP 路由、鉴权、序列化与审计上下文
internal/access                 权限目录和数据范围策略
web/src/features/hosts          主机管理页面
web/src/features/services       服务实例、配置、生命周期和日志页面
api/openapi.yaml                API 契约
```

应用层不拼 SQL，仓储层不处理 HTTP，远端层不决定业务权限。主机架构和校验信息只由主机仓储写入；服务实例仓储只维护部署实例。

## 4. 数据库

核心表：

- `hosts`：主机身份、加密凭据、指纹、架构和最近校验时间；
- `service_instances`：通过 `host_id` 关联主机；
- `service_configs`、`service_config_revisions`：按 `service_instance_id` 隔离；
- `operations`：保存服务实例、账号、主机名称/IP 和服务类型快照，实例删除后历史仍可查；
- `models`、`model_uploads`、`model_tasks`：通过 `host_id` 关联目标主机；
- `packages`、`package_versions`：按账号和服务类型管理安装包；
- `resource_tags`、`service_instance_tags`：服务实例标签；
- 用户、角色、权限、审计、通知和通讯表。

数据库只支持从空 PostgreSQL 实例执行当前迁移。旧 SQLite、旧环境表和旧接口均无兼容层。

## 5. API

### 主机

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/POST | `/api/v1/hosts` | 查询、新建主机 |
| PUT/DELETE | `/api/v1/hosts/{id}` | 修改、删除主机 |
| POST | `/api/v1/hosts/validate` | 保存前校验草稿主机 |
| POST | `/api/v1/hosts/{id}/validate` | 校验已保存主机并记录指纹、架构 |
| GET/POST | `/api/v1/hosts/export`、`/api/v1/hosts/import` | 导出、原子导入主机 |

### 服务实例

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/POST | `/api/v1/services` | 查询、新建服务实例 |
| PUT/DELETE | `/api/v1/services/{id}` | 修改、删除实例 |
| PUT | `/api/v1/services/{id}/tags` | 替换标签 |
| GET/PUT | `/api/v1/services/{id}/config` | 读取、保存实例配置 |
| POST | `/api/v1/services/{id}/{install\|start\|stop\|reset}` | 生命周期操作 |
| POST | `/api/v1/services/{id}/health-check` | 立即健康检查 |
| GET | `/api/v1/services/{id}/logs/stream` | SSE 实时日志 |

模型上传输入使用 `host_id`。完整请求和响应结构以 [OpenAPI](../api/openapi.yaml) 为准。

## 6. 部署与运行数据流

1. 用户可在浏览器后台并发上传多个安装包并查看各自进度；文件到达 DP 后进入归档与配置校验，通过后保存为不可变版本。同一账号、同一服务类型只允许一个进行中的浏览器上传任务。
2. 用户注册主机并执行 SSH 校验；DP 信任并保存主机指纹。
3. 用户选择主机、服务类型和安装目录创建服务实例。
4. 安装任务解密主机密码，经 SFTP 上传安装包和配置，执行包内脚本。
5. 启停、重置、日志与健康检查均由服务实例定位主机，再使用同一份主机凭据。
6. 操作状态、结构化事件和 JSONL 日志按 `service_instance_id` 保存。

配置中的 `port`（或 `server.port`）是健康检查端口，安装后固化为实例的 `health_port`；配置中的 `api_port` 是外部业务 API 端口，展示在服务管理页供网关使用。DP 只访问健康端口，业务流量不经过 DP。

## 7. 安全与授权

- SSH 密码使用 `DP_MASTER_KEY` 加密，API 永不返回密文或主机指纹；
- SSH 使用已信任主机指纹，变化时终止操作并产生高风险审计；
- 路径先规范化，远端脚本参数经过约束，归档拒绝绝对路径、上跳路径和危险链接；
- 权限默认拒绝，后端统一检查 `resource.action` 与 `own/all` 数据范围；
- 主机权限为 `host.read/write/delete/validate/import/export`；
- 服务实例管理权限为 `service.read/write/delete`，生命周期、配置、健康和日志使用独立服务权限；
- 管理员跨账号操作、删除、导入导出和指纹变化写入审计。

## 8. 删除与资源交接

- 主机存在服务实例或模型时返回 `409`；
- 已安装服务实例必须先重置，再删除；
- 删除实例保留操作和审计快照，不删除主机；
- 账号资源交接在一个事务中转移安装包、主机、服务实例和模型；运行中任务或唯一键冲突会使整个事务回滚；
- 删除账号前必须完成资源交接并撤销有效会话。

## 9. 测试要求

每次修改至少执行：

```bash
go test ./...
cd web && npm run build && npm test -- --run
```

数据库相关改动还必须在空数据库重放全部迁移，并设置 `DP_TEST_DATABASE_URL` 运行 PostgreSQL 集成测试。主机应用测试覆盖密码脱敏、连接变更使校验失效、依赖删除限制和导入导出；服务实例测试覆盖主机/安装包归属与已安装实例不可变约束。
