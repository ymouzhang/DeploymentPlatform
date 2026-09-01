package config

import "testing"

func TestAuditConfigDefaultsAndTrustedProxyValidation(t *testing.T) {
	t.Setenv("DP_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("DP_DATABASE_URL", "postgres://dp:test@localhost:5432/dp")
	t.Setenv("DP_AUDIT_RETENTION_DAYS", "")
	t.Setenv("DP_AUDIT_EXPORT_MAX_ROWS", "")
	t.Setenv("DP_TRUSTED_PROXY_CIDRS", "10.0.0.0/8,192.168.1.0/24")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.AuditRetentionDays != 180 || config.AuditExportMaxRows != 100000 || config.NotificationRetentionDays != 180 || config.OperationRetentionDays != 180 || config.PackageVersionRetention != 10 || config.UploadMaxBytes != int64(100<<30) || config.ModelUploadMaxBytes != int64(1<<40) || config.ModelUploadChunkBytes != int64(64<<20) || config.ModelTaskConcurrency != 2 {
		t.Fatalf("config=%+v", config)
	}
	t.Setenv("DP_TRUSTED_PROXY_CIDRS", "not-a-cidr")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid trusted proxy CIDR error")
	}
}

func TestModelConfigValidation(t *testing.T) {
	t.Setenv("DP_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("DP_DATABASE_URL", "postgres://dp:test@localhost:5432/dp")
	t.Setenv("DP_MODEL_UPLOAD_MAX_BYTES", "1024")
	t.Setenv("DP_MODEL_UPLOAD_CHUNK_BYTES", "2048")
	if _, err := Load(); err == nil {
		t.Fatal("expected chunk larger than upload maximum to fail")
	}
	t.Setenv("DP_MODEL_UPLOAD_CHUNK_BYTES", "512")
	t.Setenv("DP_MODEL_TASK_CONCURRENCY", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid task concurrency")
	}
}

func TestDatabaseURLIsRequired(t *testing.T) {
	t.Setenv("DP_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("DP_DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected missing database URL to fail")
	}
}
