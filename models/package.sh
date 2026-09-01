#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
module_dir="${script_dir}"

usage() {
  cat <<'EOF'
用法：
  ./package.sh [MODEL_ID]

下载模型并立即生成可上传到 DP 的 tar.gz 离线包和 SHA-256 文件。

示例：
  ./package.sh
  ./package.sh Qwen/Qwen3-8B
  MODEL_SOURCE=huggingface ./package.sh Qwen/Qwen3-8B

环境变量：
  MODEL_ID          默认模型，默认 Qwen/Qwen3-4B-Instruct-2507
  MODEL_SOURCE      modelscope（默认）或 huggingface
  MODELS_DIR        模型下载目录，默认 models/models
  MODEL_OUTPUT_DIR  离线包输出目录，默认 models/dist
  HF_ENDPOINT       Hugging Face 地址，默认 https://hf-mirror.com
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi
if [[ "$#" -gt 1 ]]; then
  echo "只能指定一个 MODEL_ID" >&2
  usage >&2
  exit 2
fi

model_id="${1:-${MODEL_ID:-Qwen/Qwen3-4B-Instruct-2507}}"
model_source="${MODEL_SOURCE:-modelscope}"
models_dir="${MODELS_DIR:-${module_dir}/models}"
output_dir="${MODEL_OUTPUT_DIR:-${module_dir}/dist}"
hf_endpoint="${HF_ENDPOINT:-https://hf-mirror.com}"
model_name="${model_id##*/}"

case "${model_source}" in
  modelscope|huggingface) ;;
  *) echo "MODEL_SOURCE 只能为 modelscope 或 huggingface：${model_source}" >&2; exit 2 ;;
esac
if [[ ! "${model_id}" =~ ^[0-9A-Za-z._-]+/[0-9A-Za-z._-]+$ || "${model_name}" == "." || "${model_name}" == ".." ]]; then
  echo "MODEL_ID 必须使用 owner/model 格式：${model_id}" >&2
  exit 2
fi

for command_name in curl python3 tar gzip sha256sum; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "缺少命令：${command_name}" >&2
    exit 1
  fi
done

model_dir="${models_dir}/${model_name}"
archive_name="model-${model_name}.tar.gz"
archive_path="${output_dir}/${archive_name}"
list_file="$(mktemp)"
parsed_file="$(mktemp)"
archive_tmp=""
cleanup() {
  rm -f -- "${list_file}" "${parsed_file}"
  if [[ -n "${archive_tmp}" ]]; then
    rm -f -- "${archive_tmp}"
  fi
}
trap cleanup EXIT

mkdir -p "${model_dir}" "${output_dir}"

echo "==> 获取模型清单：${model_id}（${model_source}）"
if [[ "${model_source}" == "modelscope" ]]; then
  manifest_url="https://modelscope.cn/api/v1/models/${model_id}/repo/files?Recursive=true"
else
  manifest_url="${hf_endpoint}/api/models/${model_id}/tree/main?recursive=true"
fi
if ! curl -fsSL --retry 3 --retry-delay 3 --max-time 60 -o "${list_file}" "${manifest_url}"; then
  echo "模型清单下载失败，请检查模型 ID 和网络：${model_id}" >&2
  exit 1
fi

if ! python3 - "${model_source}" "${list_file}" >"${parsed_file}" <<'PY'
import json
import pathlib
import sys

source = sys.argv[1]
with open(sys.argv[2], encoding="utf-8") as stream:
    data = json.load(stream)

if source == "modelscope":
    entries = ((item.get("Size") or 0, item["Path"])
               for item in data["Data"]["Files"] if item.get("Type") != "tree")
else:
    entries = ((item.get("size") or 0, item["path"])
               for item in data if item.get("type") == "file")

for size, name in entries:
    path = pathlib.PurePosixPath(name)
    if name == ".gitattributes":
        continue
    if not name or path.is_absolute() or ".." in path.parts or "\\" in name or "\0" in name:
        raise SystemExit(f"仓库包含不安全路径：{name!r}")
    print(f"{int(size)}\t{name}")
PY
then
  echo "模型清单格式不正确：${model_id}" >&2
  exit 1
fi
mapfile -t files <"${parsed_file}"
if [[ "${#files[@]}" -eq 0 ]]; then
  echo "模型清单为空或格式不正确：${model_id}" >&2
  exit 1
fi

total_size=0
remaining_size=0
for row in "${files[@]}"; do
  IFS=$'\t' read -r size relative_path <<<"${row}"
  total_size=$((total_size + size))
  destination="${model_dir}/${relative_path}"
  if [[ ! -f "${destination}" || "$(stat -c '%s' "${destination}")" != "${size}" ]]; then
    remaining_size=$((remaining_size + size))
  fi
done

available="$(df -B1 --output=avail "${model_dir}" | tail -n 1 | tr -d ' ')"
if (( remaining_size > 0 && available < remaining_size + 1073741824 )); then
  echo "模型下载空间不足：需要约 $((remaining_size / 1024 / 1024 / 1024)) GiB，可用 $((available / 1024 / 1024 / 1024)) GiB" >&2
  exit 1
fi

echo "==> 下载模型：${#files[@]} 个文件，约 $((total_size / 1024 / 1024 / 1024)) GiB"
index=0
for row in "${files[@]}"; do
  index=$((index + 1))
  IFS=$'\t' read -r size relative_path <<<"${row}"
  destination="${model_dir}/${relative_path}"
  if [[ -f "${destination}" && "$(stat -c '%s' "${destination}")" == "${size}" ]]; then
    echo "    [${index}/${#files[@]}] 已存在：${relative_path}"
    continue
  fi

  mkdir -p "$(dirname -- "${destination}")"
  if [[ "${model_source}" == "modelscope" ]]; then
    download_url="https://modelscope.cn/models/${model_id}/resolve/master/${relative_path}"
  else
    download_url="${hf_endpoint}/${model_id}/resolve/main/${relative_path}"
  fi
  echo "    [${index}/${#files[@]}] 下载：${relative_path}"
  if ! curl -fL -C - --retry 3 --retry-delay 5 -o "${destination}" "${download_url}"; then
    echo "下载失败，重新执行脚本可断点续传：${relative_path}" >&2
    exit 1
  fi
  actual_size="$(stat -c '%s' "${destination}")"
  if [[ "${actual_size}" != "${size}" ]]; then
    echo "文件大小校验失败：${relative_path}，期望 ${size}，实际 ${actual_size}" >&2
    exit 1
  fi
done

[[ -f "${model_dir}/config.json" ]] || { echo "模型缺少 config.json：${model_dir}" >&2; exit 1; }
if ! find "${model_dir}" -type f \( -name '*.safetensors' -o -name '*.bin' \) -print -quit | grep -q .; then
  echo "模型缺少权重文件（*.safetensors 或 *.bin）：${model_dir}" >&2
  exit 1
fi

available="$(df -B1 --output=avail "${output_dir}" | tail -n 1 | tr -d ' ')"
model_size="$(du -sb "${model_dir}" | awk '{print $1}')"
if (( available < model_size + 1073741824 )); then
  echo "模型打包空间不足：最多需要约 $((model_size / 1024 / 1024 / 1024)) GiB，可用 $((available / 1024 / 1024 / 1024)) GiB" >&2
  exit 1
fi

archive_tmp="$(mktemp "${output_dir}/.${archive_name}.XXXXXX")"
echo "==> 打包模型：${archive_path}"
# 保留唯一顶层目录；DP 校验后会安全地剥离该目录。
tar -C "${models_dir}" -cf - "${model_name}" | gzip -1 >"${archive_tmp}"
mv -f -- "${archive_tmp}" "${archive_path}"
archive_tmp=""
(cd "${output_dir}" && sha256sum "${archive_name}" >"${archive_name}.sha256")
chmod 644 "${archive_path}" "${archive_path}.sha256"

echo "模型离线包：${archive_path}（$(du -h "${archive_path}" | awk '{print $1}')）"
echo "校验文件：${archive_path}.sha256"
