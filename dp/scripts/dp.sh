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

require_initialized() {
  initialize
  if [[ ! -s .env ]]; then
    echo "缺少有效的 .env" >&2
    exit 1
  fi
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

	local master_key admin_password postgres_password current_uid current_gid required
  if [[ -f .env ]]; then
	for required in DP_POSTGRES_PASSWORD DP_MASTER_KEY DP_ADMIN_USERNAME DP_ADMIN_PASSWORD; do
	  if ! grep -Eq "^${required}=.+$" .env; then
		echo "现有 .env 缺少当前版本必填项 ${required}；本版本不补写旧配置，请重新初始化 .env。" >&2
		exit 1
	  fi
	done
    return
  fi
  admin_password="$(head -c 18 /dev/urandom | base64 | tr -d '\n')"
	postgres_password="$(head -c 24 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')"
  master_key="$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
  current_uid="$(id -u)"
  current_gid="$(id -g)"
  umask 077
  sed \
    -e "s|^DP_MASTER_KEY=.*$|DP_MASTER_KEY=${master_key}|" \
    -e "s|^DP_ADMIN_PASSWORD=.*$|DP_ADMIN_PASSWORD=${admin_password}|" \
	-e "s|^DP_POSTGRES_PASSWORD=.*$|DP_POSTGRES_PASSWORD=${postgres_password}|" \
    -e "s|^DP_UID=.*$|DP_UID=${current_uid}|" \
    -e "s|^DP_GID=.*$|DP_GID=${current_gid}|" \
    .env.example > .env
  echo "已生成 .env、独立主密钥和初始管理员密码，请妥善备份该文件。"
  echo "初始管理员：admin"
  echo "初始密码：${admin_password}"
}

load_offline_images() {
  if [[ -f dp-image.tar.gz ]]; then
	echo "正在加载 DP 离线镜像..."
    gzip -dc dp-image.tar.gz | docker image load
  fi
	if [[ -f postgres-image.tar.gz ]]; then
	  echo "正在加载 PostgreSQL 离线镜像..."
	  gzip -dc postgres-image.tar.gz | docker image load
	fi
}

start() {
  initialize
	load_offline_images
  if [[ -f Dockerfile ]]; then
    compose up -d --build
  else
    compose up -d --no-build
  fi
  compose ps
}

backup() {
  require_initialized
  require_command gzip
  require_command sha256sum
  require_command tar

  local target_dir="${1:-backups/dp-backup-$(date +%Y%m%d-%H%M%S)}"
  if [[ -e "${target_dir}" ]]; then
    echo "备份目标已存在：${target_dir}" >&2
    exit 1
  fi
  mkdir -p -- "$(dirname -- "${target_dir}")"
  mkdir -- "${target_dir}"
  target_dir="$(cd -- "${target_dir}" && pwd)"

  echo "正在启动 PostgreSQL 并生成一致性转储..."
  compose up -d --wait postgres
  compose exec -T postgres sh -c 'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom' \
    > "${target_dir}/database.dump"
  tar -C "${project_dir}" -czf "${target_dir}/data.tar.gz" data
  cp -- .env "${target_dir}/.env"
  (
    cd -- "${target_dir}"
    sha256sum database.dump data.tar.gz .env > SHA256SUMS
  )
  echo "备份完成：${target_dir}"
}

restore() {
  require_initialized
  require_command sha256sum
  require_command tar

  local source_dir="${1:-}"
  if [[ -z "${source_dir}" || ! -d "${source_dir}" ]]; then
    echo "用法：$0 restore <备份目录>" >&2
    exit 2
  fi
  source_dir="$(cd -- "${source_dir}" && pwd)"
  for required in database.dump data.tar.gz .env SHA256SUMS; do
    if [[ ! -f "${source_dir}/${required}" ]]; then
      echo "备份目录缺少文件：${required}" >&2
      exit 1
    fi
  done
  (
    cd -- "${source_dir}"
    sha256sum --check SHA256SUMS
  )

  local running_services confirmation expected
  running_services="$(compose ps --status running --services)"
  if [[ -n "${running_services}" ]]; then
    echo "恢复前必须停止全部服务，当前仍在运行：" >&2
    echo "${running_services}" >&2
    echo "请先执行：$0 stop" >&2
    exit 1
  fi
  expected="$(basename -- "${source_dir}")"
  echo "警告：将用 ${source_dir} 覆盖当前 PostgreSQL 数据卷、data/ 和 .env。"
  read -r -p "请输入备份目录名 ${expected} 以确认：" confirmation
  if [[ "${confirmation}" != "${expected}" ]]; then
    echo "确认不匹配，已取消恢复。" >&2
    exit 1
  fi

  compose down --volumes
  cp -- "${source_dir}/.env" .env
  mkdir -p data
  find data -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
  tar -C "${project_dir}" -xzf "${source_dir}/data.tar.gz"

  compose up -d --wait postgres
  if ! compose exec -T postgres sh -c 'exec pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner' \
    < "${source_dir}/database.dump"; then
    echo "数据库恢复失败；PostgreSQL 保持运行以便排查。" >&2
    exit 1
  fi
  compose stop postgres
  echo "恢复完成。请执行 $0 start 启动并应用当前版本 migration。"
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
  backup)
    backup "${2:-}"
    ;;
  restore)
    restore "${2:-}"
    ;;
  *)
    echo "用法：$0 {init|start|stop|restart|down|logs|status|backup [目录]|restore <目录>}" >&2
    exit 2
    ;;
esac
