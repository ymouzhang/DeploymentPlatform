package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `{
  "port": 18500,
  "api_port": 30080,
  "public_url": "http://10.20.30.40:30080",
  "images": {
    "api": "langgenius/dify-api:1.16.1",
    "web": "langgenius/dify-web:1.16.1",
    "bundled_api": "ymouzhang/dify-api:1.16.1-bundled",
    "nginx": "nginx:1.28.0",
    "busybox": "busybox:1.37.0",
    "sandbox": "langgenius/dify-sandbox:0.2.15",
    "weaviate": "semitechnologies/weaviate:1.27.0",
    "local_sandbox": "langgenius/dify-agent-local-sandbox:1.16.1",
    "redis": "redis:6.2.20-alpine",
    "plugin_daemon": "langgenius/dify-plugin-daemon:0.6.3-local",
    "postgres": "postgres:15.14-alpine",
    "agent_backend": "langgenius/dify-agent-backend:1.16.1",
    "squid": "ubuntu/squid:6.6-24.04_beta"
  },
  "database": {"name":"dify","user":"postgres","password":"DatabasePassword_123456789"},
  "redis_password": "RedisPassword_123456789012",
  "secret_key": "SecretKey_1234567890123456",
  "sandbox_api_key": "SandboxKey_123456789012345",
  "plugin_daemon_key": "PluginDaemon_1234567890123",
  "plugin_inner_api_key": "PluginInner_12345678901234",
  "agent_api_token": "AgentToken_123456789012345",
  "agent_server_secret_key": "AgentSecret_12345678901234",
  "agent_shell_auth_token": "ShellToken_123456789012345",
  "weaviate_api_key": "WeaviateKey_1234567890123",
  "weaviate_admin_user": "dify-admin",
  "server_workers": 2,
  "celery_workers": 4,
  "private_network_allowlist": ["10.20.0.0/16", "192.168.1.0/24"]
}`

func TestLoadAndWriteEnv(t *testing.T) {
	cfg, err := Load(writeConfig(t, validConfig), true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteEnv(&output, cfg); err != nil {
		t.Fatal(err)
	}
	env := output.String()
	for _, expected := range []string{
		"SERVICE_PORT='18500'",
		"API_PORT='30080'",
		"MARKETPLACE_ENABLED='false'",
		"SSRF_PROXY_ALLOW_PRIVATE_IPS='10.20.0.0/16 192.168.1.0/24'",
		"NEXT_PUBLIC_SOCKET_URL='ws://10.20.30.40:30080'",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("env missing %q:\n%s", expected, env)
		}
	}
}

func TestWriteImagesHasFixedThirteenImages(t *testing.T) {
	cfg, err := Load(writeConfig(t, validConfig), true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteImages(&output, cfg); err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(output.String())
	if len(lines) != 13 {
		t.Fatalf("images=%d\n%s", len(lines), output.String())
	}
	if lines[2] != "ymouzhang/dify-api:1.16.1-bundled" {
		t.Fatalf("bundled image=%q", lines[2])
	}
}

func TestRejectsPortURLMismatchAndOpenInternet(t *testing.T) {
	mismatch := strings.Replace(validConfig, ":30080\"", ":30081\"", 1)
	if _, err := Load(writeConfig(t, mismatch), false); err == nil || !strings.Contains(err.Error(), "api_port") {
		t.Fatalf("mismatch error=%v", err)
	}
	openInternet := strings.Replace(validConfig, `"10.20.0.0/16"`, `"0.0.0.0/0"`, 1)
	if _, err := Load(writeConfig(t, openInternet), false); err == nil || !strings.Contains(err.Error(), "整个互联网") {
		t.Fatalf("allowlist error=%v", err)
	}
}

func TestRejectsUnknownFieldAndPlaceholderSecret(t *testing.T) {
	unknown := strings.Replace(validConfig, `"port": 18500,`, `"port": 18500, "unknown": true,`, 1)
	if _, err := Load(writeConfig(t, unknown), false); err == nil {
		t.Fatal("expected unknown field error")
	}
	placeholder := strings.Replace(validConfig, "SecretKey_1234567890123456", "change-me-secret-key-value", 1)
	if _, err := Load(writeConfig(t, placeholder), true); err == nil || !strings.Contains(err.Error(), "占位值") {
		t.Fatalf("placeholder error=%v", err)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}
