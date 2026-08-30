#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")"

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 docker 命令" >&2
  exit 1
fi
docker compose version >/dev/null

echo "正在停止 Dify；持久化数据目录将保留..."
if [ -f .env ]; then
  docker compose -f docker-compose.yaml -f docker-compose.dp.yaml --env-file .env down --remove-orphans
else
  docker compose -f docker-compose.yaml -f docker-compose.dp.yaml down --remove-orphans
fi

