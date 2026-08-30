package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `{
  "port": 8000,
  "api_port": 8001,
  "engine": "vllm",
  "image": "vllm/vllm-openai:v0.27.1",
  "model_path": "/opt/models/Qwen 3",
  "served_model_name": "qwen",
  "api_key": "secret",
  "max_model_len": 32768,
  "gpu_memory_utilization": 0.9,
  "max_num_seqs": 32,
  "tensor_parallel_size": 1,
  "dtype": "auto",
  "extra_args": []
}`

func TestReadConfig(t *testing.T) {
	name := writeTestConfig(t, validConfig)
	cfg, err := readConfig(name, "vllm")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8000 || cfg.APIPort != 8001 || cfg.ModelPath != "/opt/models/Qwen 3" {
		t.Fatalf("cfg=%+v", cfg)
	}
	if got := quoteEnv(cfg.ModelPath); got != `"/opt/models/Qwen 3"` {
		t.Fatalf("quoteEnv()=%q", got)
	}
}

func TestReadConfigRejectsWrongEngineAndUnknownField(t *testing.T) {
	name := writeTestConfig(t, validConfig)
	if _, err := readConfig(name, "sglang"); err == nil || !strings.Contains(err.Error(), "engine") {
		t.Fatalf("wrong engine error=%v", err)
	}
	unknown := strings.Replace(validConfig, `"port": 8000,`, `"port": 8000, "unknown": true,`, 1)
	if _, err := readConfig(writeTestConfig(t, unknown), "vllm"); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}
