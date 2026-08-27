# DP Demo 服务

这是一个符合 DP 安装包约定的 Go HTTP 示例服务，通过 Docker Compose 部署。

## 接口

- `GET /`：服务信息；
- `GET /health`：返回 `{"status":"health"}`，供 DP 判断运行状态。

监听端口来自 `config/config.json` 顶层的 `port` 字段，默认 `38081`。

## 在源码目录运行

```bash
cd demo
./start.sh
curl http://127.0.0.1:38081/health
./stop.sh
```

首次启动会先编译 Go 程序，再通过 Docker Compose 构建并启动容器。

## 生成 DP 安装包

```bash
cd demo
./build-package.sh
```

产物：

```text
dist/dp-demo-linux-amd64.tar.gz
dist/dp-demo-linux-amd64.tar.gz.sha256
```

在 DP 的“安装包管理”中上传 `.tar.gz`，服务类型建议填写 `dp-demo`。随后创建环境并安装，
DP 会解压安装包并执行 `start.sh`，脚本通过 Docker Compose 启动 Demo 容器。

目标服务器要求：

- Linux；
- Docker Engine；
- Docker Compose v2；
- SSH 用户有权执行 Docker。

修改实例配置中的端口后，需要在服务管理页面重新点击“启动”，`start.sh` 会重建容器并读取
最新配置。

## 手工验证

生成安装包后也可以在 `demo/dist` 之外的临时目录解压并运行：

```bash
./start.sh
curl http://127.0.0.1:38081/health
./stop.sh
```
