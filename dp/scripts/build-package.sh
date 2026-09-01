#!/usr/bin/env bash
set -Eeuo pipefail

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令：$1" >&2
    exit 1
  fi
}

require_command docker
require_command gzip
require_command sha256sum
docker compose version >/dev/null

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_dir="$(cd -- "${script_dir}/.." && pwd)"
cd "${project_dir}"

version="${1:-$(git describe --tags --always --dirty 2>/dev/null || date +%Y%m%d%H%M%S)}"
if [[ ! "${version}" =~ ^[0-9A-Za-z._-]+$ ]]; then
  echo "版本号只能包含字母、数字、点、下划线和连字符：${version}" >&2
  exit 2
fi

platform="${DP_PLATFORM:-linux/amd64}"
case "${platform}" in
  linux/amd64|linux/arm64) ;;
  *)
    echo "当前仅支持 linux/amd64 和 linux/arm64：${platform}" >&2
    exit 2
    ;;
esac
architecture="${platform#linux/}"
image_tag="dp:${version,,}"
postgres_image="${DP_POSTGRES_IMAGE:-postgres:17-alpine}"
output_dir="${DP_OUTPUT_DIR:-${project_dir}/dist}"
mkdir -p "${output_dir}"
output_dir="$(cd -- "${output_dir}" && pwd)"
package_name="dp-${version}-linux-${architecture}"
archive_path="${output_dir}/${package_name}.tar.gz"
stage_dir="$(mktemp -d)"

cleanup() {
  if [[ -n "${stage_dir:-}" && -d "${stage_dir}" ]]; then
    rm -rf -- "${stage_dir}"
  fi
}
trap cleanup EXIT

echo "==> 构建并测试 ${image_tag} (${platform})"
DP_IMAGE="${image_tag}" \
DP_PLATFORM="${platform}" \
DP_MASTER_KEY="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
DP_ADMIN_USERNAME="admin" \
DP_ADMIN_PASSWORD="build-only-password" \
DP_POSTGRES_PASSWORD="build-only-postgres-password" \
  docker compose build --pull

echo "==> 拉取 PostgreSQL 镜像 ${postgres_image} (${platform})"
docker pull --platform "${platform}" "${postgres_image}"

bundle_dir="${stage_dir}/${package_name}"
mkdir -p "${bundle_dir}/data"

echo "==> 导出离线镜像"
docker image save "${image_tag}" | gzip -9 > "${bundle_dir}/dp-image.tar.gz"
docker image save "${postgres_image}" | gzip -9 > "${bundle_dir}/postgres-image.tar.gz"

cp compose.yaml "${bundle_dir}/compose.yaml"
cp scripts/dp.sh "${bundle_dir}/dp.sh"
cp deploy/README.md "${bundle_dir}/README.md"
sed \
  -e "s|^DP_IMAGE=.*$|DP_IMAGE=${image_tag}|" \
  -e "s|^DP_POSTGRES_IMAGE=.*$|DP_POSTGRES_IMAGE=${postgres_image}|" \
  -e "s|^DP_PLATFORM=.*$|DP_PLATFORM=${platform}|" \
  .env.example > "${bundle_dir}/.env.example"
chmod +x "${bundle_dir}/dp.sh"

echo "==> 生成部署包"
tar -C "${stage_dir}" -czf "${archive_path}" "${package_name}"
archive_name="$(basename -- "${archive_path}")"
(cd "${output_dir}" && sha256sum "${archive_name}") > "${archive_path}.sha256"

echo "部署包：${archive_path}"
echo "校验文件：${archive_path}.sha256"
