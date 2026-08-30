#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")"

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 docker 命令" >&2
  exit 1
fi
docker compose version >/dev/null

if [ ! -x ./dp-litellm-config ]; then
  echo "缺少可执行文件 dp-litellm-config，请使用 package.sh 生成的安装包" >&2
  exit 1
fi

runtime_env=".dp-runtime.env"
runtime_config=".dp-litellm-config.json"
env_tmp="${runtime_env}.tmp.$$"
config_tmp="${runtime_config}.tmp.$$"
trap 'rm -f -- "${env_tmp}" "${config_tmp}"' EXIT HUP INT TERM

./dp-litellm-config --require-secrets --output env config/config.json >"${env_tmp}"
./dp-litellm-config --require-secrets --output litellm config/config.json >"${config_tmp}"
chmod 600 "${env_tmp}" "${config_tmp}"
mv -f -- "${env_tmp}" "${runtime_env}"
mv -f -- "${config_tmp}" "${runtime_config}"
trap - EXIT HUP INT TERM

./load_offline_images.sh

echo "正在启动 LiteLLM 网关和 PostgreSQL..."
docker compose --env-file "${runtime_env}" up -d --pull never --force-recreate --remove-orphans --wait --wait-timeout 300
docker compose --env-file "${runtime_env}" ps
service_port="$(sed -n 's/^SERVICE_PORT=//p' "${runtime_env}")"
api_port="$(sed -n 's/^API_PORT=//p' "${runtime_env}")"
echo "LiteLLM 已启动；网关端口：${api_port}，DP 健康检查端口：${service_port}"
