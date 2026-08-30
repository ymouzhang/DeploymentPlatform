package operation

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/archive"
	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
	"DP/internal/store"
)

func TestManagerWaitsForCanceledOperation(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dataDir, "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cipher, err := security.NewPasswordCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{
		OwnerID: store.InitialAdminID, Name: "test", IP: "192.0.2.40", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: encrypted, InstallDir: "/opt/demo", ServiceType: "demo", Installed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	manager := NewManager(workerCtx, dataDir, db, cipher, archive.NewManager(dataDir, 1<<20, db),
		remote.NewExecutor(time.Second), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	op, err := manager.Start(ctx, env.ID, domain.ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	manager.Wait()
	if manager.Busy(env.ID) {
		t.Fatal("environment remains busy after Wait")
	}
	stored, err := db.GetOperation(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !isTerminal(stored.Status) {
		t.Fatalf("operation status = %q", stored.Status)
	}
}

func isTerminal(status domain.OperationStatus) bool {
	switch status {
	case domain.OperationSucceeded, domain.OperationFailed, domain.OperationTimedOut, domain.OperationInterrupted:
		return true
	default:
		return false
	}
}
