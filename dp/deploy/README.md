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

1. 创建持久化目录 `data/`；
2. 生成权限为 `600` 的 `.env`；
3. 生成用于 SSH 密码加密的独立主密钥；
4. 生成随机初始管理员密码并打印一次（同时保存在 `.env`）；
5. 加载离线镜像并通过 Docker Compose 启动。

浏览器访问 `http://<服务器IP>:30199`。如需修改端口，启动前编辑 `.env`
中的 `DP_HTTP_PORT`。

## 运维命令

```bash
./dp.sh status
./dp.sh logs
./dp.sh restart
./dp.sh stop
./dp.sh down
```

`down` 只删除容器和网络，不删除 `data/`。

## 必须备份

- `.env`：包含主密钥；丢失或更换后，已有 SSH 密码无法解密。
- `data/`：包含 SQLite 数据库、安装包和操作日志。

升级前请同时备份这两项。迁移到另一台服务器时，将它们与部署文件一起复制。

`.env` 中可通过 `DP_PACKAGE_VERSION_RETENTION` 设置每个服务类型的安装包版本保留目标数量，通过 `DP_OPERATION_RETENTION_DAYS` 和 `DP_NOTIFICATION_RETENTION_DAYS` 设置终态操作日志及已处理通知的保留天数，通过 `DP_STALE_ACCOUNT_DAYS` 设置长期未登录账号提醒阈值（默认 90 天）。当前或被环境引用的安装包版本、运行中操作和未处理通知不会被自动清理。
