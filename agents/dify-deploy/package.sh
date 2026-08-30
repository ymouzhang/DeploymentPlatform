#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../.." && pwd)"
dify_source="${repo_root}/agents/dify"
cd "${script_dir}"

for command_name in docker go gzip tar sha256sum python3; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "缺少命令：${command_name}" >&2
    exit 1
  fi
done
docker compose version >/dev/null

if [[ ! -f "${dify_source}/api/Dockerfile" || ! -f "${dify_source}/docker/docker-compose.yaml" ]]; then
  echo "Dify 子模块未初始化，请先执行 git submodule update --init agents/dify" >&2
  exit 1
fi

target_arch="${DIFY_ARCH:-amd64}"
if [[ "${target_arch}" != "amd64" ]]; then
  echo "当前 bundled 离线插件仅支持 amd64，DIFY_ARCH 必须为 amd64" >&2
  exit 2
fi

bundle_images="${DIFY_BUNDLE_IMAGES:-1}"
build_bundled_image="${DIFY_BUILD_BUNDLED_IMAGE:-1}"
pull_policy="${DIFY_PULL_IMAGES:-missing}"
gzip_level="${DIFY_GZIP_LEVEL:-6}"
build_network="${DIFY_BUILD_NETWORK:-}"
case "${bundle_images}" in 0|1) ;; *) echo "DIFY_BUNDLE_IMAGES 只能为 0 或 1" >&2; exit 2 ;; esac
case "${build_bundled_image}" in 0|1) ;; *) echo "DIFY_BUILD_BUNDLED_IMAGE 只能为 0 或 1" >&2; exit 2 ;; esac
case "${pull_policy}" in always|missing|never) ;; *) echo "DIFY_PULL_IMAGES 只能为 always、missing 或 never" >&2; exit 2 ;; esac
case "${gzip_level}" in 1|2|3|4|5|6|7|8|9) ;; *) echo "DIFY_GZIP_LEVEL 必须为 1 到 9" >&2; exit 2 ;; esac
if [[ -z "${build_network}" ]]; then
  build_network="default"
  if [[ "${HTTP_PROXY:-}${HTTPS_PROXY:-}" == *"127.0.0.1:"* || "${HTTP_PROXY:-}${HTTPS_PROXY:-}" == *"localhost:"* ]]; then
    build_network="host"
  fi
fi
case "${build_network}" in default|host) ;; *) echo "DIFY_BUILD_NETWORK 只能为 default 或 host" >&2; exit 2 ;; esac

output_dir="${DIFY_OUTPUT_DIR:-${script_dir}/dist}"
mkdir -p "${output_dir}"
output_dir="$(cd -- "${output_dir}" && pwd)"
stage_dir="$(mktemp -d "${output_dir}/.package-stage.XXXXXX")"
host_configctl="${stage_dir}/dp-dify-config-host"
target_configctl="${stage_dir}/dp-dify-config"
package_name="dp-dify"
package_root="${stage_dir}/${package_name}"
archive_path="${output_dir}/${package_name}-linux-${target_arch}.tar.gz"

cleanup() {
  if [[ -n "${stage_dir:-}" && -d "${stage_dir}" ]]; then
    rm -rf -- "${stage_dir}"
  fi
}
trap cleanup EXIT

echo "==> 验证 bundled 离线插件制品"
(cd "${dify_source}" && python3 api/bundled_plugins/package_plugins.py verify)

echo "==> 测试并编译 Dify 配置工具"
(cd configctl && go test ./...)
(cd configctl && go build -trimpath -o "${host_configctl}" ./cmd/configctl)
(cd configctl && CGO_ENABLED=0 GOOS=linux GOARCH="${target_arch}" \
  go build -trimpath -ldflags="-s -w" -o "${target_configctl}" ./cmd/configctl)

mapfile -t image_refs < <("${host_configctl}" --output images config/config.json)
if [[ "${#image_refs[@]}" -ne 13 ]]; then
  echo "Dify 固定组合必须声明 13 张唯一镜像，当前为 ${#image_refs[@]}" >&2
  exit 1
fi
bundled_image_ref="$("${host_configctl}" --output images config/config.json | sed -n '3p')"

if [[ "${bundle_images}" == "1" ]]; then
  if [[ "${build_bundled_image}" == "1" ]]; then
    echo "==> 从 feature/1.16.1 源码构建 bundled worker 镜像"
    build_options=(--progress plain --network "${build_network}" --platform "linux/${target_arch}")
    for proxy_name in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
      proxy_value="${!proxy_name:-}"
      if [[ -n "${proxy_value}" ]]; then
        build_options+=(--build-arg "${proxy_name}=${proxy_value}")
      fi
    done
    docker build "${build_options[@]}" -f "${dify_source}/api/Dockerfile" \
      -t "${bundled_image_ref}" "${dify_source}"
  fi

  for image_ref in "${image_refs[@]}"; do
    if [[ "${image_ref}" != "${bundled_image_ref}" ]]; then
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
    fi

    if ! docker image inspect "${image_ref}" >/dev/null 2>&1; then
      echo "本机不存在镜像 ${image_ref}" >&2
      if [[ "${image_ref}" == "${bundled_image_ref}" ]]; then
        echo "请启用 DIFY_BUILD_BUNDLED_IMAGE=1 或提前构建该镜像" >&2
      fi
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
cp "${target_configctl}" "${package_root}/dp-dify-config"
cp config/config.json "${package_root}/config/config.json"
cp "${dify_source}/docker/docker-compose.yaml" "${package_root}/docker-compose.yaml"
cp docker-compose.dp.yaml start.sh stop.sh README.md "${package_root}/"
cp runtime/health_server.py runtime/load_offline_images.sh "${package_root}/"
cp -R "${dify_source}/docker/nginx" "${package_root}/nginx"
cp -R "${dify_source}/docker/ssrf_proxy" "${package_root}/ssrf_proxy"
mkdir -p "${package_root}/envs"
cp -R "${dify_source}/docker/envs/." "${package_root}/envs/"

chmod 755 "${package_root}" "${package_root}/config" "${package_root}/dp-dify-config" \
  "${package_root}/start.sh" "${package_root}/stop.sh" "${package_root}/health_server.py" \
  "${package_root}/load_offline_images.sh"
chmod 644 "${package_root}/config/config.json" "${package_root}/docker-compose.yaml" \
  "${package_root}/docker-compose.dp.yaml" "${package_root}/README.md"

"${host_configctl}" --output env config/config.json >"${package_root}/.env"
chmod 600 "${package_root}/.env"
(cd "${package_root}" && docker compose -f docker-compose.yaml -f docker-compose.dp.yaml --env-file .env config --quiet)
rm -f -- "${package_root}/.env"

if [[ "${bundle_images}" == "1" ]]; then
  : >"${package_root}/offline-images.manifest"
  for image_ref in "${image_refs[@]}"; do
    image_id="$(docker image inspect "${image_ref}" --format '{{.Id}}')"
    printf '%s|%s\n' "${image_ref}" "${image_id}" >>"${package_root}/offline-images.manifest"
  done
  echo "==> 导出 13 张 Dify 离线镜像（压缩级别 ${gzip_level}）"
  docker image save "${image_refs[@]}" | gzip "-${gzip_level}" >"${package_root}/offline-images.tar.gz"
  chmod 644 "${package_root}/offline-images.manifest" "${package_root}/offline-images.tar.gz"
else
  echo "==> 跳过镜像导出（该包不能保证离线部署）"
fi

tar -C "${stage_dir}" -czf "${archive_path}" "${package_name}"
archive_name="$(basename -- "${archive_path}")"
(cd "${output_dir}" && sha256sum "${archive_name}") >"${archive_path}.sha256"
chmod 644 "${archive_path}" "${archive_path}.sha256"

echo "安装包：${archive_path}（服务类型建议填写：dify）"
