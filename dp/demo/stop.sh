#!/usr/bin/env sh
set -eu

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 docker 命令" >&2
  exit 1
fi
docker compose version >/dev/null

echo "正在停止 DP Demo..."
docker compose down --remove-orphans
