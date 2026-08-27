#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "${script_dir}/../compose.yaml" ]]; then
  project_dir="$(cd -- "${script_dir}/.." && pwd)"
else
  project_dir="${script_dir}"
fi

cd "${project_dir}"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令：$1" >&2
    exit 1
  fi
}

compose() {
  docker compose --env-file .env "$@"
}

initialize() {
  require_command docker
  require_command base64
  require_command head
  docker compose version >/dev/null
  mkdir -p data

  if [[ ! -f .env.example ]]; then
    echo "缺少 .env.example，无法初始化配置" >&2
    exit 1
  fi

  local master_key admin_password current_uid current_gid
  admin_password="$(head -c 18 /dev/urandom | base64 | tr -d '\n')"
  if [[ -f .env ]]; then
    if ! grep -q '^DP_ADMIN_USERNAME=' .env || ! grep -q '^DP_ADMIN_PASSWORD=' .env; then
      umask 077
      {
        echo
        echo 'DP_ADMIN_USERNAME=admin'
        echo "DP_ADMIN_PASSWORD=${admin_password}"
        echo 'DP_SESSION_TTL=24h'
        echo 'DP_STALE_ACCOUNT_DAYS=90'
        echo 'DP_AUDIT_RETENTION_DAYS=180'
        echo 'DP_AUDIT_EXPORT_MAX_ROWS=100000'
        echo 'DP_NOTIFICATION_RETENTION_DAYS=180'
        echo 'DP_OPERATION_RETENTION_DAYS=180'
        echo 'DP_PACKAGE_VERSION_RETENTION=10'
        echo '# DP_TRUSTED_PROXY_CIDRS=10.0.0.0/8'
      } >> .env
      echo "检测到旧版 .env，已追加初始管理员：admin"
      echo "初始密码：${admin_password}"
      echo "请立即备份 .env，并在首次登录后修改密码。"
    fi
    return
  fi
  master_key="$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
  current_uid="$(id -u)"
  current_gid="$(id -g)"
  umask 077
  sed \
    -e "s|^DP_MASTER_KEY=.*$|DP_MASTER_KEY=${master_key}|" \
    -e "s|^DP_ADMIN_PASSWORD=.*$|DP_ADMIN_PASSWORD=${admin_password}|" \
    -e "s|^DP_UID=.*$|DP_UID=${current_uid}|" \
    -e "s|^DP_GID=.*$|DP_GID=${current_gid}|" \
    .env.example > .env
  echo "已生成 .env、独立主密钥和初始管理员密码，请妥善备份该文件。"
  echo "初始管理员：admin"
  echo "初始密码：${admin_password}"
}

load_offline_image() {
  if [[ -f dp-image.tar.gz ]]; then
    echo "正在加载离线镜像..."
    gzip -dc dp-image.tar.gz | docker image load
  fi
}

start() {
  initialize
  load_offline_image
  if [[ -f Dockerfile ]]; then
    compose up -d --build
  else
    compose up -d --no-build
  fi
  compose ps
}

case "${1:-start}" in
  init)
    initialize
    ;;
  start|up)
    start
    ;;
  stop)
    initialize
    compose stop
    ;;
  restart)
    initialize
    compose restart
    compose ps
    ;;
  down)
    initialize
    compose down
    ;;
  logs)
    initialize
    compose logs -f --tail=200
    ;;
  status|ps)
    initialize
    compose ps
    ;;
  *)
    echo "用法：$0 {init|start|stop|restart|down|logs|status}" >&2
    exit 2
    ;;
esac
