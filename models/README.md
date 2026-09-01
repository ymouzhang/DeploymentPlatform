# 模型离线包

脚本从 ModelScope（默认）或 Hugging Face 拉取模型，校验文件大小后立即生成 DP“模型管理”可上传的
`.tar.gz` 和 `.sha256` 文件。

在项目根目录执行：

```bash
# 默认拉取 Qwen/Qwen3-4B-Instruct-2507
./package.sh --module models

# 指定模型
MODEL_ID=Qwen/Qwen3-8B ./package.sh --module models

# 使用 Hugging Face；国内默认使用 ModelScope
MODEL_SOURCE=huggingface MODEL_ID=Qwen/Qwen3-8B ./package.sh --module models
```

结果统一输出到根目录 `dist/model-<模型名>.tar.gz`，下载文件缓存在 `models/models/<模型名>/`；重新执行
会跳过大小一致的文件，并续传未完成文件。

也可以在本目录直接执行：

```bash
./package.sh Qwen/Qwen3-8B
```

此时结果默认输出到 `models/dist/`。
