#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "用法: $0 <vllm|sglang>" >&2
  exit 2
fi

engine="$1"
configured_image="$(./dp-inference-config --engine "${engine}" --output image config/config.json)"
archive="inference-image.tar.gz"
reference_file="offline-image.ref"
id_file="offline-image.id"

if [ -f "${archive}" ]; then
  if [ ! -f "${reference_file}" ] || [ ! -f "${id_file}" ]; then
    echo "离线镜像元数据不完整" >&2
    exit 1
  fi
  bundled_image="$(sed -n '1p' "${reference_file}")"
  bundled_id="$(sed -n '1p' "${id_file}")"
  if [ "${configured_image}" != "${bundled_image}" ]; then
    echo "配置镜像 ${configured_image} 与离线包镜像 ${bundled_image} 不一致" >&2
    echo "离线环境不允许修改 config/config.json 中的 image" >&2
    exit 1
  fi
  current_id="$(docker image inspect "${configured_image}" --format '{{.Id}}' 2>/dev/null || true)"
  if [ "${current_id}" != "${bundled_id}" ]; then
    echo "正在加载离线推理镜像：${bundled_image}"
    docker image load --input "${archive}"
  else
    echo "离线推理镜像已加载，跳过重复导入"
  fi
  current_id="$(docker image inspect "${configured_image}" --format '{{.Id}}' 2>/dev/null || true)"
  if [ "${current_id}" != "${bundled_id}" ]; then
    echo "离线镜像加载后 ID 校验失败：${configured_image}" >&2
    exit 1
  fi
elif ! docker image inspect "${configured_image}" >/dev/null 2>&1; then
  echo "安装包不含离线镜像，目标机也不存在 ${configured_image}" >&2
  exit 1
fi
