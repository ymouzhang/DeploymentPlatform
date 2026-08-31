package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"DP/internal/application"
	"DP/internal/archive"
	"DP/internal/audit"
	"DP/internal/config"
	"DP/internal/health"
	"DP/internal/httpapi"
	modelmanager "DP/internal/model"
	"DP/internal/operation"
	"DP/internal/realtime"
	"DP/internal/remote"
	"DP/internal/security"
	"DP/internal/store"
	"DP/webui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "DP startup failed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(rootCtx, filepath.Join(cfg.DataDir, "dp.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	authService := application.NewAuthService(db, cfg.SessionTTL)
	realtimeHub := realtime.NewHub(64)
	communicationService := application.NewCommunicationService(db, realtimeHub)
	if err := authService.InitializeAdmin(rootCtx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return fmt.Errorf("initialize admin account: %w", err)
	}
	passwordCipher, err := security.NewPasswordCipher(cfg.MasterKey)
	if err != nil {
		return err
	}
	remoteExecutor := remote.NewExecutor(cfg.UploadTimeout)
	packageManager := archive.NewManager(cfg.DataDir, cfg.UploadMaxBytes, db)
	packageManager.ConfigureRetention(cfg.PackageVersionRetention)
	environmentService := application.NewEnvironmentService(db, passwordCipher, remoteExecutor)
	serviceConfigService := application.NewServiceConfigService(
		db, packageManager, passwordCipher, remoteExecutor,
	)
	serviceLogService := application.NewServiceLogService(db, passwordCipher, remoteExecutor)
	auditService := audit.NewService(db, cfg.AuditRetentionDays, log)
	auditService.ConfigureMaintenance(cfg.DataDir, cfg.NotificationRetentionDays, cfg.OperationRetentionDays, cfg.StaleAccountDays)
	operationManager := operation.NewManager(
		rootCtx, cfg.DataDir, db, passwordCipher, packageManager, remoteExecutor, auditService, log,
	)
	modelManager := modelmanager.NewManager(
		rootCtx, cfg.DataDir, db, passwordCipher, remoteExecutor, auditService,
		cfg.ModelUploadMaxBytes, cfg.ModelUploadChunkBytes, cfg.ModelUploadRetention,
		cfg.ModelTransferTimeout, cfg.ModelTaskConcurrency, log,
	)
	healthMonitor := health.NewMonitor(db, cfg.DataDir, cfg.HealthInterval)
	var background sync.WaitGroup
	background.Add(3)
	go func() {
		defer background.Done()
		healthMonitor.Run(rootCtx)
	}()
	go func() {
		defer background.Done()
		auditService.Run(rootCtx)
	}()
	go func() {
		defer background.Done()
		modelManager.Run(rootCtx)
	}()

	dist, err := fs.Sub(webui.Files, "dist")
	if err == nil {
		_, err = fs.Stat(dist, "index.html")
	}
	if err != nil {
		dist, err = fs.Sub(webui.Files, ".")
		if err != nil {
			return fmt.Errorf("load embedded frontend: %w", err)
		}
	}
	api := httpapi.New(
		authService,
		communicationService,
		realtimeHub,
		environmentService, serviceConfigService, serviceLogService, packageManager, operationManager, modelManager, healthMonitor,
		db, auditService, cfg.UploadMaxBytes, cfg.AuditExportMaxRows, cfg.TrustedProxyCIDRs, log,
	)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.Handler(dist),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      0,
		IdleTimeout:       75 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("DP started", "address", cfg.ListenAddr, "data_dir", cfg.DataDir)
		errCh <- server.ListenAndServe()
	}()
	var runErr error
	select {
	case <-rootCtx.Done():
	case err := <-errCh:
		if err != http.ErrServerClosed {
			runErr = err
		}
		stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := server.Shutdown(shutdownCtx)
	cancel()
	if shutdownErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("shutdown HTTP server: %w", shutdownErr))
	}
	operationManager.Wait()
	modelManager.Wait()
	background.Wait()
	return runErr
}
