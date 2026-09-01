# DP Docker 部署包

## 要求

- Linux 服务器
- Docker Engine
- Docker Compose v2

## 启动

```bash
./dp.sh start
```

首次启动会自动：

1. 创建持久化目录 `data/` 和 PostgreSQL 持久卷；
2. 生成权限为 `600` 的 `.env`；
3. 生成用于 SSH 密码加密的独立主密钥；
4. 生成随机初始管理员密码并打印一次（同时保存在 `.env`）；
5. 加载 DP 与 PostgreSQL 两个离线镜像并通过 Docker Compose 启动。

浏览器访问 `http://<服务器IP>:30199`。如需修改端口，启动前编辑 `.env`
中的 `DP_HTTP_PORT`。

## 运维命令

```bash
./dp.sh status
./dp.sh logs
./dp.sh restart
./dp.sh stop
./dp.sh down
./dp.sh backup
./dp.sh restore backups/<备份目录>
```

`down` 只删除容器和网络，不删除 `data/` 或 PostgreSQL 持久卷。

`backup` 默认在 `backups/` 下生成带时间戳的目录，目录中包含 PostgreSQL 自定义格式转储、
`data/` 归档、`.env` 副本和 SHA-256 校验文件。`restore` 会覆盖当前数据库与 `data/`，因此要求
DP 和 PostgreSQL 容器均已停止，并要求输入目标目录名进行确认；恢复后再执行 `./dp.sh start`。

## 必须备份

- `.env`：包含主密钥；丢失或更换后，已有 SSH 密码无法解密。
- PostgreSQL 持久卷：包含账号、RBAC、资源和审计等业务数据；应使用 `pg_dump` 定期备份。
- `data/`：包含安装包、模型任务文件和操作日志，不再保存数据库。

升级前请同时备份这三项。迁移到另一台服务器时，将完整备份目录与部署文件一起复制。

`.env` 中可通过 `DP_PACKAGE_VERSION_RETENTION` 设置每个服务类型的安装包版本保留目标数量，通过 `DP_OPERATION_RETENTION_DAYS` 和 `DP_NOTIFICATION_RETENTION_DAYS` 设置终态操作日志及已处理通知的保留天数，通过 `DP_STALE_ACCOUNT_DAYS` 设置长期未登录账号提醒阈值（默认 90 天）。当前或被环境引用的安装包版本、运行中操作和未处理通知不会被自动清理。

模型离线上传按分片经 DP 直接写入所选环境的目标机，完整模型包不会落到 DP 的 `data/`。目标机在解压期间
需要同时容纳压缩包、展开目录和安全余量。可通过 `DP_MODEL_UPLOAD_MAX_BYTES`、
`DP_MODEL_UPLOAD_CHUNK_BYTES`、`DP_MODEL_UPLOAD_RETENTION`、`DP_MODEL_TRANSFER_TIMEOUT` 和
`DP_MODEL_TASK_CONCURRENCY` 调整模型上传限制、会话保留时间及任务并发。
