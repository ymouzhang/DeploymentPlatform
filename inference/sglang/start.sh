#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")"

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 docker 命令" >&2
  exit 1
fi
docker compose version >/dev/null

if [ ! -x ./dp-inference-config ]; then
  echo "缺少可执行文件 dp-inference-config，请使用 inference/package.sh 生成的安装包" >&2
  exit 1
fi

runtime_env=".dp-runtime.env"
runtime_tmp="${runtime_env}.tmp.$$"
trap 'rm -f -- "${runtime_tmp}"' EXIT HUP INT TERM
./dp-inference-config --engine sglang config/config.json >"${runtime_tmp}"
chmod 600 "${runtime_tmp}"
mv -f -- "${runtime_tmp}" "${runtime_env}"
trap - EXIT HUP INT TERM

./load_offline_image.sh sglang

echo "正在启动 SGLang 推理服务（模型就绪过程在容器后台继续）..."
docker compose --env-file "${runtime_env}" up -d --pull never --force-recreate --remove-orphans
docker compose --env-file "${runtime_env}" ps
service_port="$(sed -n 's/^SERVICE_PORT=//p' "${runtime_env}")"
api_port="$(sed -n 's/^API_PORT=//p' "${runtime_env}")"
echo "SGLang 已提交启动；LiteLLM 直连端口：${api_port}，DP 健康检查端口：${service_port}"
