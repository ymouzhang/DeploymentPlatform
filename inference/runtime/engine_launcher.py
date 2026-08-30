#!/usr/bin/env python3
"""Turn the DP JSON configuration into the selected engine's argv."""

import json
import os
import sys


def require(config, name, expected_type):
    value = config.get(name)
    if not isinstance(value, expected_type) or isinstance(value, bool) and expected_type is int:
        raise ValueError(f"{name} 类型无效")
    return value


def common_args(config):
    served_name = require(config, "served_model_name", str).strip()
    if not served_name:
        raise ValueError("served_model_name 不能为空")
    args = [
        "--host", "0.0.0.0",
        "--port", "8000",
        "--served-model-name", served_name,
    ]
    api_key = config.get("api_key", "")
    if api_key:
        if not isinstance(api_key, str):
            raise ValueError("api_key 类型无效")
        args += ["--api-key", api_key]
    return args


def positive_int(config, name):
    value = require(config, name, int)
    if value <= 0:
        raise ValueError(f"{name} 必须大于 0")
    return str(value)


def ratio(config, name):
    value = config.get(name)
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not 0 < value <= 1:
        raise ValueError(f"{name} 必须大于 0 且不超过 1")
    return str(value)


def build_vllm(config):
    args = [sys.executable, "-m", "vllm.entrypoints.openai.api_server", "--model", "/models"]
    args += common_args(config)
    args += [
        "--max-model-len", positive_int(config, "max_model_len"),
        "--gpu-memory-utilization", ratio(config, "gpu_memory_utilization"),
        "--max-num-seqs", positive_int(config, "max_num_seqs"),
        "--tensor-parallel-size", positive_int(config, "tensor_parallel_size"),
        "--dtype", require(config, "dtype", str),
    ]
    if config.get("enable_prefix_caching", False):
        args.append("--enable-prefix-caching")
    if config.get("enable_auto_tool_choice", False):
        args.append("--enable-auto-tool-choice")
        parser = require(config, "tool_call_parser", str).strip()
        if not parser:
            raise ValueError("启用自动工具调用时 tool_call_parser 不能为空")
        args += ["--tool-call-parser", parser]
    return args


def build_sglang(config):
    args = [sys.executable, "-m", "sglang.launch_server", "--model-path", "/models"]
    args += common_args(config)
    args += [
        "--context-length", positive_int(config, "max_model_len"),
        "--mem-fraction-static", ratio(config, "gpu_memory_utilization"),
        "--max-running-requests", positive_int(config, "max_num_seqs"),
        "--tp-size", positive_int(config, "tensor_parallel_size"),
        "--dtype", require(config, "dtype", str),
    ]
    parser = config.get("tool_call_parser", "")
    if parser:
        if not isinstance(parser, str):
            raise ValueError("tool_call_parser 类型无效")
        args += ["--tool-call-parser", parser]
    if config.get("enable_metrics", False):
        args.append("--enable-metrics")
    return args


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in {"vllm", "sglang"}:
        raise SystemExit("用法: engine_launcher.py <vllm|sglang> config.json")
    with open(sys.argv[2], "r", encoding="utf-8") as handle:
        config = json.load(handle)
    if config.get("engine") != sys.argv[1]:
        raise ValueError(f"配置 engine 必须为 {sys.argv[1]}")
    extra_args = config.get("extra_args", [])
    if not isinstance(extra_args, list) or not all(isinstance(item, str) for item in extra_args):
        raise ValueError("extra_args 必须是字符串数组")
    command = build_vllm(config) if sys.argv[1] == "vllm" else build_sglang(config)
    command += extra_args
    os.execv(command[0], command)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"推理服务配置错误: {error}", file=sys.stderr)
        raise SystemExit(2) from error
