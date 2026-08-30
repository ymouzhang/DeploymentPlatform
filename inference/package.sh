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

target_arch="${INFERENCE_ARCH:-$(go env GOARCH)}"
case "${target_arch}" in
  amd64|arm64) ;;
  *)
    echo "当前仅支持 amd64 和 arm64：${target_arch}" >&2
    exit 2
    ;;
esac

output_dir="${INFERENCE_OUTPUT_DIR:-${script_dir}/dist}"
mkdir -p "${output_dir}"
# 推理镜像通常为数 GB，不能依赖容量较小的 /tmp tmpfs。
stage_dir="$(mktemp -d "${output_dir}/.package-stage.XXXXXX")"
host_configctl="${stage_dir}/dp-inference-config-host"
target_configctl="${stage_dir}/dp-inference-config"
configctl_dir="${script_dir}/configctl"

bundle_images="${INFERENCE_BUNDLE_IMAGES:-1}"
pull_policy="${INFERENCE_PULL_IMAGES:-missing}"
gzip_level="${INFERENCE_GZIP_LEVEL:-6}"
case "${bundle_images}" in 0|1) ;; *) echo "INFERENCE_BUNDLE_IMAGES 只能为 0 或 1" >&2; exit 2 ;; esac
case "${pull_policy}" in always|missing|never) ;; *) echo "INFERENCE_PULL_IMAGES 只能为 always、missing 或 never" >&2; exit 2 ;; esac
case "${gzip_level}" in 1|2|3|4|5|6|7|8|9) ;; *) echo "INFERENCE_GZIP_LEVEL 必须为 1 到 9" >&2; exit 2 ;; esac

cleanup() {
  if [[ -n "${stage_dir:-}" && -d "${stage_dir}" ]]; then
    rm -rf -- "${stage_dir}"
  fi
}
trap cleanup EXIT

echo "==> 测试配置转换工具"
(cd "${configctl_dir}" && go test ./...)

echo "==> 编译 linux/${target_arch} 配置转换工具"
(cd "${configctl_dir}" && go build -trimpath -o "${host_configctl}" ./cmd/configctl)
(cd "${configctl_dir}" && CGO_ENABLED=0 GOOS=linux GOARCH="${target_arch}" \
  go build -trimpath -ldflags="-s -w" -o "${target_configctl}" ./cmd/configctl)

for engine in vllm sglang; do
  package_name="dp-${engine}"
  package_root="${stage_dir}/${package_name}"
  archive_path="${output_dir}/${package_name}-linux-${target_arch}.tar.gz"
  image_ref="$("${host_configctl}" --engine "${engine}" --output image "${engine}/config/config.json")"

  if [[ "${bundle_images}" == "1" ]]; then
    if [[ "${pull_policy}" == "always" ]] || \
      { [[ "${pull_policy}" == "missing" ]] && ! docker image inspect "${image_ref}" >/dev/null 2>&1; }; then
      echo "==> 拉取 ${engine} 镜像：${image_ref} (linux/${target_arch})"
      docker pull --platform "linux/${target_arch}" "${image_ref}"
    fi
    if ! docker image inspect "${image_ref}" >/dev/null 2>&1; then
      echo "本机不存在镜像 ${image_ref}；请先 docker pull，或设置 INFERENCE_PULL_IMAGES=always" >&2
      exit 1
    fi
    image_os="$(docker image inspect "${image_ref}" --format '{{.Os}}')"
    image_arch="$(docker image inspect "${image_ref}" --format '{{.Architecture}}')"
    if [[ "${image_os}/${image_arch}" != "linux/${target_arch}" ]]; then
      echo "镜像平台 ${image_os}/${image_arch} 与安装包 linux/${target_arch} 不一致：${image_ref}" >&2
      exit 1
    fi
  fi

  mkdir -p "${package_root}/config"
  cp "${target_configctl}" "${package_root}/dp-inference-config"
  cp "${engine}/docker-compose.yaml" "${package_root}/docker-compose.yaml"
  cp "${engine}/config/config.json" "${package_root}/config/config.json"
  cp "${engine}/start.sh" "${engine}/stop.sh" "${package_root}/"
  cp "${script_dir}/runtime/engine_launcher.py" "${script_dir}/runtime/health_server.py" \
    "${script_dir}/runtime/load_offline_image.sh" "${package_root}/"
  chmod 755 "${package_root}" "${package_root}/config"
  chmod 644 "${package_root}/docker-compose.yaml" "${package_root}/config/config.json"
  chmod 755 "${package_root}/dp-inference-config" "${package_root}/start.sh" \
    "${package_root}/stop.sh" "${package_root}/engine_launcher.py" "${package_root}/health_server.py" \
    "${package_root}/load_offline_image.sh"

  "${host_configctl}" --engine "${engine}" "${engine}/config/config.json" >/dev/null
  if [[ "${bundle_images}" == "1" ]]; then
    image_id="$(docker image inspect "${image_ref}" --format '{{.Id}}')"
    printf '%s\n' "${image_ref}" > "${package_root}/offline-image.ref"
    printf '%s\n' "${image_id}" > "${package_root}/offline-image.id"
    echo "==> 导出 ${engine} 离线镜像（压缩级别 ${gzip_level}）：${image_ref}"
    docker image save "${image_ref}" | gzip "-${gzip_level}" > "${package_root}/inference-image.tar.gz"
    chmod 644 "${package_root}/offline-image.ref" "${package_root}/offline-image.id" \
      "${package_root}/inference-image.tar.gz"
  else
    echo "==> ${engine} 跳过镜像导出（该包不能保证离线部署）"
  fi
  tar -C "${stage_dir}" -czf "${archive_path}" "${package_name}"
  archive_name="$(basename -- "${archive_path}")"
  (cd "${output_dir}" && sha256sum "${archive_name}") > "${archive_path}.sha256"
  chmod 644 "${archive_path}" "${archive_path}.sha256"
  rm -rf -- "${package_root}"

  echo "安装包：${archive_path}（服务类型建议填写：${engine}）"
done
