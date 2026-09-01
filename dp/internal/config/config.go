package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr              = "127.0.0.1:8080"
	defaultDataDir                 = "./data"
	defaultHealthInterval          = 10 * time.Second
	defaultUploadTimeout           = 10 * time.Minute
	defaultUploadMaxBytes          = int64(100 << 30)
	defaultSessionTTL              = 24 * time.Hour
	defaultAuditRetention          = int64(180)
	defaultAuditExportMax          = int64(100000)
	defaultNotificationRetention   = int64(180)
	defaultOperationRetention      = int64(180)
	defaultPackageVersionRetention = int64(10)
	defaultStaleAccountDays        = int64(90)
	defaultModelUploadMaxBytes     = int64(1 << 40)
	defaultModelUploadChunkBytes   = int64(64 << 20)
	defaultModelUploadRetention    = 72 * time.Hour
	defaultModelTransferTimeout    = 24 * time.Hour
	defaultModelTaskConcurrency    = int64(2)
)

type Config struct {
	ListenAddr                string
	DataDir                   string
	MasterKey                 []byte
	HealthInterval            time.Duration
	UploadTimeout             time.Duration
	UploadMaxBytes            int64
	AdminUsername             string
	AdminPassword             string
	SessionTTL                time.Duration
	AuditRetentionDays        int
	AuditExportMaxRows        int
	NotificationRetentionDays int
	OperationRetentionDays    int
	PackageVersionRetention   int
	StaleAccountDays          int
	TrustedProxyCIDRs         string
	LogLevel                  slog.Level
	ModelUploadMaxBytes       int64
	ModelUploadChunkBytes     int64
	ModelUploadRetention      time.Duration
	ModelTransferTimeout      time.Duration
	ModelTaskConcurrency      int
}

func Load() (Config, error) {
	var cfg Config
	cfg.ListenAddr = envOr("DP_LISTEN_ADDR", defaultListenAddr)
	cfg.DataDir = envOr("DP_DATA_DIR", defaultDataDir)

	keyText := strings.TrimSpace(os.Getenv("DP_MASTER_KEY"))
	if keyText == "" {
		return Config{}, errors.New("DP_MASTER_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(keyText)
	if err != nil || len(key) != 32 {
		return Config{}, errors.New("DP_MASTER_KEY must be Base64-encoded 32 bytes")
	}
	cfg.MasterKey = key
	cfg.AdminUsername = strings.TrimSpace(os.Getenv("DP_ADMIN_USERNAME"))
	cfg.AdminPassword = os.Getenv("DP_ADMIN_PASSWORD")
	if cfg.SessionTTL, err = durationEnv("DP_SESSION_TTL", defaultSessionTTL); err != nil {
		return Config{}, err
	}
	auditRetention, err := int64Env("DP_AUDIT_RETENTION_DAYS", defaultAuditRetention)
	if err != nil || auditRetention <= 0 {
		return Config{}, errors.New("DP_AUDIT_RETENTION_DAYS must be a positive integer")
	}
	auditExportMax, err := int64Env("DP_AUDIT_EXPORT_MAX_ROWS", defaultAuditExportMax)
	if err != nil || auditExportMax <= 0 {
		return Config{}, errors.New("DP_AUDIT_EXPORT_MAX_ROWS must be a positive integer")
	}
	cfg.AuditRetentionDays = int(auditRetention)
	cfg.AuditExportMaxRows = int(auditExportMax)
	notificationRetention, err := int64Env("DP_NOTIFICATION_RETENTION_DAYS", defaultNotificationRetention)
	if err != nil || notificationRetention <= 0 {
		return Config{}, errors.New("DP_NOTIFICATION_RETENTION_DAYS must be a positive integer")
	}
	operationRetention, err := int64Env("DP_OPERATION_RETENTION_DAYS", defaultOperationRetention)
	if err != nil || operationRetention <= 0 {
		return Config{}, errors.New("DP_OPERATION_RETENTION_DAYS must be a positive integer")
	}
	packageRetention, err := int64Env("DP_PACKAGE_VERSION_RETENTION", defaultPackageVersionRetention)
	if err != nil || packageRetention <= 0 {
		return Config{}, errors.New("DP_PACKAGE_VERSION_RETENTION must be a positive integer")
	}
	staleAccountDays, err := int64Env("DP_STALE_ACCOUNT_DAYS", defaultStaleAccountDays)
	if err != nil || staleAccountDays <= 0 {
		return Config{}, errors.New("DP_STALE_ACCOUNT_DAYS must be a positive integer")
	}
	cfg.NotificationRetentionDays = int(notificationRetention)
	cfg.OperationRetentionDays = int(operationRetention)
	cfg.PackageVersionRetention = int(packageRetention)
	cfg.StaleAccountDays = int(staleAccountDays)
	cfg.TrustedProxyCIDRs = strings.TrimSpace(os.Getenv("DP_TRUSTED_PROXY_CIDRS"))
	for _, value := range strings.Split(cfg.TrustedProxyCIDRs, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, err := netip.ParsePrefix(value); err != nil {
			return Config{}, fmt.Errorf("DP_TRUSTED_PROXY_CIDRS contains invalid CIDR %q", value)
		}
	}

	if cfg.HealthInterval, err = durationEnv("DP_HEALTH_INTERVAL", defaultHealthInterval); err != nil {
		return Config{}, err
	}
	if cfg.UploadTimeout, err = durationEnv("DP_UPLOAD_TIMEOUT", defaultUploadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.UploadMaxBytes, err = int64Env("DP_UPLOAD_MAX_BYTES", defaultUploadMaxBytes); err != nil {
		return Config{}, err
	}
	if cfg.UploadMaxBytes <= 0 {
		return Config{}, errors.New("DP_UPLOAD_MAX_BYTES must be positive")
	}
	if cfg.ModelUploadMaxBytes, err = int64Env("DP_MODEL_UPLOAD_MAX_BYTES", defaultModelUploadMaxBytes); err != nil || cfg.ModelUploadMaxBytes <= 0 {
		return Config{}, errors.New("DP_MODEL_UPLOAD_MAX_BYTES must be a positive integer")
	}
	if cfg.ModelUploadChunkBytes, err = int64Env("DP_MODEL_UPLOAD_CHUNK_BYTES", defaultModelUploadChunkBytes); err != nil || cfg.ModelUploadChunkBytes <= 0 || cfg.ModelUploadChunkBytes > cfg.ModelUploadMaxBytes {
		return Config{}, errors.New("DP_MODEL_UPLOAD_CHUNK_BYTES must be positive and not exceed DP_MODEL_UPLOAD_MAX_BYTES")
	}
	if cfg.ModelUploadRetention, err = durationEnv("DP_MODEL_UPLOAD_RETENTION", defaultModelUploadRetention); err != nil {
		return Config{}, err
	}
	if cfg.ModelTransferTimeout, err = durationEnv("DP_MODEL_TRANSFER_TIMEOUT", defaultModelTransferTimeout); err != nil {
		return Config{}, err
	}
	modelConcurrency, err := int64Env("DP_MODEL_TASK_CONCURRENCY", defaultModelTaskConcurrency)
	if err != nil || modelConcurrency <= 0 || modelConcurrency > 64 {
		return Config{}, errors.New("DP_MODEL_TASK_CONCURRENCY must be between 1 and 64")
	}
	cfg.ModelTaskConcurrency = int(modelConcurrency)

	switch strings.ToLower(envOr("DP_LOG_LEVEL", "info")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("invalid DP_LOG_LEVEL")
	}
	return cfg, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

func int64Env(name string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return parsed, nil
}
