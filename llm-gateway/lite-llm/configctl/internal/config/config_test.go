package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `{
  "port": 18400,
  "api_port": 4000,
  "litellm_image": "ghcr.io/berriai/litellm:v1.98.0",
  "postgres_image": "postgres:16.15-trixie",
  "master_key": "sk-a-real-master-key",
  "salt_key": "a-real-immutable-salt-key",
  "database": {"name": "litellm", "user": "litellm", "password": "a-real-db-password"},
  "store_model_in_db": true,
  "num_workers": 1,
  "request_timeout_seconds": 600,
  "database_pool_limit": 10,
  "proxy_batch_write_seconds": 60,
  "models": [{
    "model_name": "qwen3",
    "model": "openai/Qwen3",
    "api_base": "http://host.docker.internal:8000/v1",
    "api_key": "upstream-key"
  }]
}`

func TestLoadAndOutputs(t *testing.T) {
	cfg, err := Load(writeTestConfig(t, validConfig), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 18400 || cfg.APIPort != 4000 || len(cfg.Models) != 1 {
		t.Fatalf("cfg=%+v", cfg)
	}
	var output bytes.Buffer
	if err := WriteLiteLLM(&output, cfg); err != nil {
		t.Fatal(err)
	}
	var runtime liteLLMConfig
	if err := json.Unmarshal(output.Bytes(), &runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.ModelList[0].LiteLLMParams.APIBase != "http://host.docker.internal:8000/v1" || !runtime.LiteLLMSettings.JSONLogs {
		t.Fatalf("runtime=%+v", runtime)
	}
}

func TestLoadRejectsUnknownDuplicateAndPlaceholder(t *testing.T) {
	unknown := strings.Replace(validConfig, `"port": 18400,`, `"port": 18400, "unknown": true,`, 1)
	if _, err := Load(writeTestConfig(t, unknown), false); err == nil {
		t.Fatal("expected unknown field error")
	}
	duplicate := strings.Replace(validConfig, `"models": [`, `"models": [{"model_name":"qwen3","model":"openai/x","api_base":"http://127.0.0.1/v1","api_key":"x"},`, 1)
	if _, err := Load(writeTestConfig(t, duplicate), false); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("duplicate error=%v", err)
	}
	placeholder := strings.Replace(validConfig, "sk-a-real-master-key", "sk-change-me-before-production", 1)
	if _, err := Load(writeTestConfig(t, placeholder), true); err == nil || !strings.Contains(err.Error(), "占位值") {
		t.Fatalf("placeholder error=%v", err)
	}
}

func TestDatabaseURLAndEnvironmentOutput(t *testing.T) {
	cfg, err := Load(writeTestConfig(t, strings.Replace(validConfig, "a-real-db-password", "password-with-@-sign", 1)), true)
	if err != nil {
		t.Fatal(err)
	}
	if value := databaseURL(cfg); !strings.Contains(value, "password-with-%40-sign") {
		t.Fatalf("database URL=%q", value)
	}
	var output bytes.Buffer
	if err := WriteEnv(&output, cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "DATABASE_URL='postgresql://") {
		t.Fatalf("env=%q", output.String())
	}
}

func TestWriteImages(t *testing.T) {
	cfg, err := Load(writeTestConfig(t, validConfig), true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteImages(&output, cfg); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "ghcr.io/berriai/litellm:v1.98.0\npostgres:16.15-trixie\n" {
		t.Fatalf("images=%q", got)
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
