#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

for command_name in go tar sha256sum; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "缺少命令：${command_name}" >&2
    exit 1
  fi
done

target_arch="${DEMO_ARCH:-$(go env GOARCH)}"
case "${target_arch}" in
  amd64|arm64) ;;
  *)
    echo "当前仅支持 amd64 和 arm64：${target_arch}" >&2
    exit 2
    ;;
esac

output_dir="${script_dir}/dist"
stage_dir="$(mktemp -d)"
package_root="${stage_dir}/dp-demo"
archive_path="${output_dir}/dp-demo-linux-${target_arch}.tar.gz"

cleanup() {
  if [[ -n "${stage_dir:-}" && -d "${stage_dir}" ]]; then
    rm -rf -- "${stage_dir}"
  fi
}
trap cleanup EXIT

mkdir -p "${package_root}/config" "${output_dir}"

echo "==> 测试 Go 服务"
go test ./...

echo "==> 编译 linux/${target_arch} 静态程序"
CGO_ENABLED=0 GOOS=linux GOARCH="${target_arch}" \
  go build -trimpath -ldflags="-s -w" -o "${package_root}/demo-service" .

cp .dockerignore Dockerfile compose.yaml start.sh stop.sh "${package_root}/"
cp config/config.json "${package_root}/config/config.json"
chmod +x "${package_root}/demo-service" "${package_root}/start.sh" "${package_root}/stop.sh"

echo "==> 生成 DP 安装包"
tar -C "${stage_dir}" -czf "${archive_path}" dp-demo
sha256sum "${archive_path}" > "${archive_path}.sha256"

echo "安装包：${archive_path}"
echo "服务类型建议填写：dp-demo"
