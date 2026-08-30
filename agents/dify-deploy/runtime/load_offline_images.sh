#!/usr/bin/env sh
set -eu

archive="offline-images.tar.gz"
manifest="offline-images.manifest"
configured_refs=".dp-configured-images.$$"
bundled_refs=".dp-bundled-images.$$"
trap 'rm -f -- "${configured_refs}" "${bundled_refs}"' EXIT HUP INT TERM

./dp-dify-config --output images config/config.json >"${configured_refs}"

if [ -f "${archive}" ]; then
  if [ ! -f "${manifest}" ]; then
    echo "离线镜像清单不存在：${manifest}" >&2
    exit 1
  fi
  awk -F '|' 'NF == 2 { print $1 }' "${manifest}" >"${bundled_refs}"
  if ! cmp -s "${configured_refs}" "${bundled_refs}"; then
    echo "配置中的镜像与离线包镜像清单不一致" >&2
    echo "离线环境不允许修改 images 中的镜像引用" >&2
    exit 1
  fi

  need_load=0
  while IFS='|' read -r image_ref expected_id; do
    if [ -z "${image_ref}" ] || [ -z "${expected_id}" ]; then
      echo "离线镜像清单格式错误" >&2
      exit 1
    fi
    current_id="$(docker image inspect "${image_ref}" --format '{{.Id}}' 2>/dev/null || true)"
    if [ "${current_id}" != "${expected_id}" ]; then
      need_load=1
    fi
  done <"${manifest}"

  if [ "${need_load}" -eq 1 ]; then
    echo "正在加载 Dify 离线镜像..."
    docker image load --input "${archive}"
  else
    echo "Dify 离线镜像均已加载，跳过重复导入"
  fi

  while IFS='|' read -r image_ref expected_id; do
    current_id="$(docker image inspect "${image_ref}" --format '{{.Id}}' 2>/dev/null || true)"
    if [ "${current_id}" != "${expected_id}" ]; then
      echo "离线镜像加载后 ID 校验失败：${image_ref}" >&2
      exit 1
    fi
  done <"${manifest}"
else
  while IFS= read -r image_ref; do
    if ! docker image inspect "${image_ref}" >/dev/null 2>&1; then
      echo "安装包不含离线镜像，目标机也不存在 ${image_ref}" >&2
      exit 1
    fi
  done <"${configured_refs}"
fi

