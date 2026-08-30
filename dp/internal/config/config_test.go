package config

import "testing"

func TestAuditConfigDefaultsAndTrustedProxyValidation(t *testing.T) {
	t.Setenv("DP_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("DP_AUDIT_RETENTION_DAYS", "")
	t.Setenv("DP_AUDIT_EXPORT_MAX_ROWS", "")
	t.Setenv("DP_TRUSTED_PROXY_CIDRS", "10.0.0.0/8,192.168.1.0/24")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.AuditRetentionDays != 180 || config.AuditExportMaxRows != 100000 || config.NotificationRetentionDays != 180 || config.OperationRetentionDays != 180 || config.PackageVersionRetention != 10 {
		t.Fatalf("config=%+v", config)
	}
	t.Setenv("DP_TRUSTED_PROXY_CIDRS", "not-a-cidr")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid trusted proxy CIDR error")
	}
}
