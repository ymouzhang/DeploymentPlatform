#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

for command_name in docker go gzip tar sha256sum; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "缺少命令：${command_name}" >&2
    exit 1
  fi
done
docker compose version >/dev/null

target_arch="${LITELLM_ARCH:-$(go env GOARCH)}"
case "${target_arch}" in
  amd64|arm64) ;;
  *)
    echo "当前仅支持 amd64 和 arm64：${target_arch}" >&2
    exit 2
    ;;
esac

bundle_images="${LITELLM_BUNDLE_IMAGES:-1}"
pull_policy="${LITELLM_PULL_IMAGES:-missing}"
gzip_level="${LITELLM_GZIP_LEVEL:-6}"
case "${bundle_images}" in 0|1) ;; *) echo "LITELLM_BUNDLE_IMAGES 只能为 0 或 1" >&2; exit 2 ;; esac
case "${pull_policy}" in always|missing|never) ;; *) echo "LITELLM_PULL_IMAGES 只能为 always、missing 或 never" >&2; exit 2 ;; esac
case "${gzip_level}" in 1|2|3|4|5|6|7|8|9) ;; *) echo "LITELLM_GZIP_LEVEL 必须为 1 到 9" >&2; exit 2 ;; esac

output_dir="${script_dir}/dist"
mkdir -p "${output_dir}"
stage_dir="$(mktemp -d "${output_dir}/.package-stage.XXXXXX")"
host_configctl="${stage_dir}/dp-litellm-config-host"
target_configctl="${stage_dir}/dp-litellm-config"
configctl_dir="${script_dir}/configctl"
package_name="dp-litellm"
package_root="${stage_dir}/${package_name}"
archive_path="${output_dir}/${package_name}-linux-${target_arch}.tar.gz"

cleanup() {
  if [[ -n "${stage_dir:-}" && -d "${stage_dir}" ]]; then
    rm -rf -- "${stage_dir}"
  fi
}
trap cleanup EXIT

echo "==> 测试 LiteLLM 配置转换工具"
(cd "${configctl_dir}" && go test ./...)

echo "==> 编译 linux/${target_arch} 配置转换工具"
(cd "${configctl_dir}" && go build -trimpath -o "${host_configctl}" ./cmd/configctl)
(cd "${configctl_dir}" && CGO_ENABLED=0 GOOS=linux GOARCH="${target_arch}" \
  go build -trimpath -ldflags="-s -w" -o "${target_configctl}" ./cmd/configctl)

mapfile -t image_refs < <("${host_configctl}" --output images config/config.json)
if [[ "${#image_refs[@]}" -ne 2 ]]; then
  echo "配置必须正好声明 LiteLLM 和 PostgreSQL 两张镜像" >&2
  exit 1
fi

if [[ "${bundle_images}" == "1" ]]; then
  for image_ref in "${image_refs[@]}"; do
    pull_image=0
    if [[ "${pull_policy}" == "always" ]]; then
      pull_image=1
    elif [[ "${pull_policy}" == "missing" ]]; then
      if ! docker image inspect "${image_ref}" >/dev/null 2>&1; then
        pull_image=1
      else
        image_os="$(docker image inspect "${image_ref}" --format '{{.Os}}')"
        image_arch="$(docker image inspect "${image_ref}" --format '{{.Architecture}}')"
        if [[ "${image_os}/${image_arch}" != "linux/${target_arch}" ]]; then
          pull_image=1
        fi
      fi
    fi
    if [[ "${pull_image}" -eq 1 ]]; then
      echo "==> 拉取镜像：${image_ref} (linux/${target_arch})"
      docker pull --platform "linux/${target_arch}" "${image_ref}"
    fi
    if ! docker image inspect "${image_ref}" >/dev/null 2>&1; then
      echo "本机不存在镜像 ${image_ref}；请先拉取或设置 LITELLM_PULL_IMAGES=always" >&2
      exit 1
    fi
    image_os="$(docker image inspect "${image_ref}" --format '{{.Os}}')"
    image_arch="$(docker image inspect "${image_ref}" --format '{{.Architecture}}')"
    if [[ "${image_os}/${image_arch}" != "linux/${target_arch}" ]]; then
      echo "镜像平台 ${image_os}/${image_arch} 与安装包 linux/${target_arch} 不一致：${image_ref}" >&2
      exit 1
    fi
  done
fi

mkdir -p "${package_root}/config"
cp "${target_configctl}" "${package_root}/dp-litellm-config"
cp docker-compose.yaml "${package_root}/docker-compose.yaml"
cp config/config.json "${package_root}/config/config.json"
cp start.sh stop.sh "${package_root}/"
cp runtime/health_server.py runtime/load_offline_images.sh "${package_root}/"
chmod 755 "${package_root}" "${package_root}/config"
chmod 644 "${package_root}/docker-compose.yaml" "${package_root}/config/config.json"
chmod 755 "${package_root}/dp-litellm-config" "${package_root}/start.sh" \
  "${package_root}/stop.sh" "${package_root}/health_server.py" "${package_root}/load_offline_images.sh"

"${host_configctl}" --output env config/config.json >/dev/null
"${host_configctl}" --output litellm config/config.json >/dev/null

if [[ "${bundle_images}" == "1" ]]; then
  : >"${package_root}/offline-images.manifest"
  for image_ref in "${image_refs[@]}"; do
    image_id="$(docker image inspect "${image_ref}" --format '{{.Id}}')"
    printf '%s|%s\n' "${image_ref}" "${image_id}" >>"${package_root}/offline-images.manifest"
  done
  echo "==> 导出 LiteLLM 和 PostgreSQL 离线镜像（压缩级别 ${gzip_level}）"
  docker image save "${image_refs[@]}" | gzip "-${gzip_level}" >"${package_root}/offline-images.tar.gz"
  chmod 644 "${package_root}/offline-images.manifest" "${package_root}/offline-images.tar.gz"
else
  echo "==> 跳过镜像导出（该包不能保证离线部署）"
fi

tar -C "${stage_dir}" -czf "${archive_path}" "${package_name}"
archive_name="$(basename -- "${archive_path}")"
(cd "${output_dir}" && sha256sum "${archive_name}") >"${archive_path}.sha256"
chmod 644 "${archive_path}" "${archive_path}.sha256"

echo "安装包：${archive_path}（服务类型建议填写：litellm）"
