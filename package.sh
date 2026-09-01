#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}"

usage() {
  cat <<'EOF'
用法：
  ./package.sh [--module MODULE]... [--version VERSION]

MODULE：
  all        打包全部模块（默认）
  dp         仅打包 Deployment Platform
  inference  打包 vLLM 和 SGLang
  vllm       仅打包 vLLM
  sglang     仅打包 SGLang
  litellm    仅打包 LiteLLM
  dify       仅打包 Dify；缺少 submodule 时自动初始化
  models     拉取模型并生成可上传到 DP 的离线包

示例：
  ./package.sh --version v1.0.0
  ./package.sh --module vllm
  ./package.sh --module litellm --module dify
  MODEL_ID=Qwen/Qwen3-8B ./package.sh --module models
  ./package.sh --module dp --version v1.0.0
EOF
}

selected_modules=()
version="${PACKAGE_VERSION:-}"
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -m|--module)
      if [[ "$#" -lt 2 ]]; then
        echo "$1 缺少模块名" >&2
        usage >&2
        exit 2
      fi
      selected_modules+=("$2")
      shift 2
      ;;
    --module=*)
      selected_modules+=("${1#*=}")
      shift
      ;;
    -v|--version)
      if [[ "$#" -lt 2 ]]; then
        echo "$1 缺少版本号" >&2
        usage >&2
        exit 2
      fi
      version="$2"
      shift 2
      ;;
    --version=*)
      version="${1#*=}"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数：$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${#selected_modules[@]}" -eq 0 ]]; then
  selected_modules=(all)
fi

declare -A requested=()
for module_name in "${selected_modules[@]}"; do
  case "${module_name}" in
    all)
      requested[dp]=1
      requested[vllm]=1
      requested[sglang]=1
      requested[litellm]=1
      requested[dify]=1
      requested[models]=1
      ;;
    inference)
      requested[vllm]=1
      requested[sglang]=1
      ;;
    dp|vllm|sglang|litellm|dify|models)
      requested["${module_name}"]=1
      ;;
    *)
      echo "不支持的模块：${module_name}" >&2
      usage >&2
      exit 2
      ;;
  esac
done

target_arch="${PACKAGE_ARCH:-amd64}"
case "${target_arch}" in
  amd64|arm64) ;;
  *)
    echo "PACKAGE_ARCH 只能为 amd64 或 arm64：${target_arch}" >&2
    exit 2
    ;;
esac
if [[ -n "${requested[dify]:-}" && "${target_arch}" != "amd64" ]]; then
  echo "Dify bundled 插件仅支持 amd64；打包 Dify 时 PACKAGE_ARCH 必须为 amd64" >&2
  exit 2
fi

if [[ -z "${version}" ]]; then
  version="$(git describe --tags --always --dirty 2>/dev/null || date +%Y%m%d%H%M%S)"
fi
if [[ ! "${version}" =~ ^[0-9A-Za-z._-]+$ ]]; then
  echo "版本号只能包含字母、数字、点、下划线和连字符：${version}" >&2
  exit 2
fi

output_dir="${PACKAGE_OUTPUT_DIR:-${script_dir}/dist}"
mkdir -p "${output_dir}"
output_dir="$(cd -- "${output_dir}" && pwd)"
generated_archives=()

if [[ -n "${requested[dify]:-}" ]]; then
  if ! command -v git >/dev/null 2>&1; then
    echo "打包 Dify 需要 git 来初始化 submodule" >&2
    exit 1
  fi
  if [[ ! -f agents/dify/api/Dockerfile || ! -f agents/dify/docker/docker-compose.yaml ]]; then
    echo "==> 初始化 Dify submodule（父仓库锁定提交）"
    git submodule sync -- agents/dify
    git submodule update --init --recursive agents/dify
  fi
  if [[ ! -f agents/dify/api/Dockerfile || ! -f agents/dify/docker/docker-compose.yaml ]]; then
    echo "Dify submodule 初始化后仍不完整：agents/dify" >&2
    exit 1
  fi
fi

if [[ -n "${requested[models]:-}" ]]; then
  model_id="${MODEL_ID:-Qwen/Qwen3-4B-Instruct-2507}"
  model_name="${model_id##*/}"
  echo "==> 拉取并打包模型：${model_id}"
  MODEL_OUTPUT_DIR="${output_dir}" \
    ./models/package.sh "${model_id}"
  generated_archives+=("model-${model_name}.tar.gz")
fi

if [[ -n "${requested[dp]:-}" ]]; then
  echo "==> 打包 DP 平台"
  DP_PLATFORM="linux/${target_arch}" \
  DP_OUTPUT_DIR="${output_dir}" \
    ./dp/scripts/build-package.sh "${version}"
  generated_archives+=("dp-${version}-linux-${target_arch}.tar.gz")
fi

inference_engines=()
if [[ -n "${requested[vllm]:-}" ]]; then
  inference_engines+=(vllm)
fi
if [[ -n "${requested[sglang]:-}" ]]; then
  inference_engines+=(sglang)
fi
if [[ "${#inference_engines[@]}" -gt 0 ]]; then
  echo "==> 打包推理引擎：${inference_engines[*]}"
  INFERENCE_ARCH="${target_arch}" \
  INFERENCE_ENGINES="${inference_engines[*]}" \
  INFERENCE_OUTPUT_DIR="${output_dir}" \
    ./inference/package.sh
  for engine_name in "${inference_engines[@]}"; do
    generated_archives+=("dp-${engine_name}-linux-${target_arch}.tar.gz")
  done
fi

if [[ -n "${requested[litellm]:-}" ]]; then
  echo "==> 打包 LiteLLM"
  LITELLM_ARCH="${target_arch}" \
  LITELLM_OUTPUT_DIR="${output_dir}" \
    ./llm-gateway/lite-llm/package.sh
  generated_archives+=("dp-litellm-linux-${target_arch}.tar.gz")
fi

if [[ -n "${requested[dify]:-}" ]]; then
  echo "==> 打包 Dify"
  DIFY_ARCH="${target_arch}" \
  DIFY_OUTPUT_DIR="${output_dir}" \
    ./agents/dify-deploy/package.sh
  generated_archives+=("dp-dify-linux-${target_arch}.tar.gz")
fi

echo "==> 校验本次生成的独立安装包"
for archive_name in "${generated_archives[@]}"; do
  archive_path="${output_dir}/${archive_name}"
  checksum_path="${archive_path}.sha256"
  if [[ ! -s "${archive_path}" || ! -s "${checksum_path}" ]]; then
    echo "缺少打包制品或校验文件：${archive_path}" >&2
    exit 1
  fi
  (cd "${output_dir}" && sha256sum -c "${archive_name}.sha256")
done

echo "打包完成；各模块保持为独立压缩包，未生成总压缩包："
for archive_name in "${generated_archives[@]}"; do
  archive_path="${output_dir}/${archive_name}"
  printf '  %s  %s\n' "$(du -h "${archive_path}" | awk '{print $1}')" "${archive_path}"
done
