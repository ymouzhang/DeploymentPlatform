#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")"

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 docker 命令" >&2
  exit 1
fi
docker compose version >/dev/null

echo "正在停止 LiteLLM 网关..."
if [ -f .dp-runtime.env ]; then
  docker compose --env-file .dp-runtime.env down --remove-orphans
else
  docker compose down --remove-orphans
fi
