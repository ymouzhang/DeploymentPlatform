#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")"

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 docker 命令" >&2
  exit 1
fi
docker compose version >/dev/null
if [ ! -x ./dp-dify-config ]; then
  echo "缺少 dp-dify-config，请使用 package.sh 生成的安装包" >&2
  exit 1
fi

runtime_env=".env"
env_tmp="${runtime_env}.tmp.$$"
trap 'rm -f -- "${env_tmp}"' EXIT HUP INT TERM
./dp-dify-config --require-secrets --output env config/config.json >"${env_tmp}"
chmod 600 "${env_tmp}"
mv -f -- "${env_tmp}" "${runtime_env}"
trap - EXIT HUP INT TERM

mkdir -p volumes/app/storage volumes/db/data volumes/redis/data volumes/weaviate \
  volumes/sandbox/dependencies volumes/sandbox/conf volumes/plugin_daemon nginx/ssl

./load_offline_images.sh

compose() {
  docker compose -f docker-compose.yaml -f docker-compose.dp.yaml --env-file "${runtime_env}" "$@"
}

echo "正在启动 Dify 完整离线服务..."
compose config --quiet
compose up -d --pull never --no-build --force-recreate --remove-orphans

attempt=0
while [ "${attempt}" -lt 180 ]; do
  initializer_id="$(compose ps -a -q plugin_initializer 2>/dev/null || true)"
  if [ -n "${initializer_id}" ]; then
    initializer_status="$(docker inspect --format '{{.State.Status}}' "${initializer_id}")"
    if [ "${initializer_status}" = "exited" ]; then
      initializer_code="$(docker inspect --format '{{.State.ExitCode}}' "${initializer_id}")"
      if [ "${initializer_code}" != "0" ]; then
        echo "离线插件初始化失败，退出码：${initializer_code}" >&2
        compose logs --no-color --tail 100 plugin_initializer >&2 || true
        exit 1
      fi
    fi
  fi

  health_id="$(compose ps -q health 2>/dev/null || true)"
  if [ -n "${health_id}" ]; then
    health_status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${health_id}")"
    if [ "${health_status}" = "healthy" ]; then
      compose ps
      service_port="$(sed -n "s/^SERVICE_PORT='\(.*\)'$/\1/p" "${runtime_env}")"
      api_port="$(sed -n "s/^API_PORT='\(.*\)'$/\1/p" "${runtime_env}")"
      echo "Dify 已启动；业务端口：${api_port}，DP 健康检查端口：${service_port}"
      exit 0
    fi
    if [ "${health_status}" = "unhealthy" ]; then
      echo "Dify 健康检查失败" >&2
      compose logs --no-color --tail 150 health api nginx local_sandbox agent_backend >&2 || true
      exit 1
    fi
  fi
  attempt=$((attempt + 1))
  sleep 5
done

echo "等待 Dify 就绪超时" >&2
compose ps >&2 || true
compose logs --no-color --tail 150 health api nginx plugin_initializer local_sandbox agent_backend >&2 || true
exit 1

