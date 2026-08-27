#!/usr/bin/env sh
set -eu

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 docker 命令" >&2
  exit 1
fi
docker compose version >/dev/null

if [ ! -x ./demo-service ]; then
  if ! command -v go >/dev/null 2>&1; then
    echo "缺少 demo-service，请先执行 ./build-package.sh 或安装 Go" >&2
    exit 1
  fi
  echo "正在编译 Demo 程序..."
  CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o demo-service .
fi

echo "正在构建并启动 DP Demo..."
docker compose up -d --build --force-recreate --remove-orphans --wait --wait-timeout 60
docker compose ps
