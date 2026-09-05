package application

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
)

type hostStoreStub struct {
	host        domain.Host
	hasServices bool
	hasModels   bool
	created     int
	updated     int
	deleted     bool
}

func (s *hostStoreStub) CreateHost(_ context.Context, host domain.Host) (domain.Host, error) {
	host.ID = "host-1"
	s.host = host
	s.created++
	return host, nil
}
func (s *hostStoreStub) UpdateHost(_ context.Context, host domain.Host) (domain.Host, error) {
	s.host = host
	s.updated++
	return host, nil
}
func (s *hostStoreStub) DeleteHost(context.Context, string) error { s.deleted = true; return nil }
func (s *hostStoreStub) GetHost(context.Context, string) (domain.Host, error) {
	if s.host.ID == "" {
		return domain.Host{}, domain.ErrNotFound
	}
	return s.host, nil
}
func (s *hostStoreStub) ListHosts(context.Context, string) ([]domain.Host, error) {
	if s.host.ID == "" {
		return nil, nil
	}
	return []domain.Host{s.host}, nil
}
func (s *hostStoreStub) UpsertHosts(_ context.Context, hosts []domain.Host) (int, int, error) {
	if len(hosts) > 0 {
		s.host = hosts[0]
	}
	return s.created, s.updated, nil
}
func (*hostStoreStub) RecordHostValidation(context.Context, string, string, string) error { return nil }
func (s *hostStoreStub) HostHasServiceInstances(context.Context, string) (bool, error) {
	return s.hasServices, nil
}
func (s *hostStoreStub) HostHasModels(context.Context, string) (bool, error) { return s.hasModels, nil }

type hostValidatorStub struct {
	result   remote.ValidationResult
	err      error
	calls    int
	host     domain.Host
	password string
}

func (s *hostValidatorStub) ValidateHost(_ context.Context, host domain.Host, password []byte) (remote.ValidationResult, error) {
	s.calls++
	s.host = host
	s.password = string(password)
	return s.result, s.err
}

func testHostCipher(t *testing.T) *security.PasswordCipher {
	t.Helper()
	cipher, err := security.NewPasswordCipher(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestHostCreateEncryptsPasswordAndHidesSecrets(t *testing.T) {
	store := &hostStoreStub{}
	validator := &hostValidatorStub{result: remote.ValidationResult{Fingerprint: "SHA256:test", Arch: "amd64"}}
	view, err := NewHostService(store, testHostCipher(t), validator).Create(context.Background(), "owner", domain.HostInput{
		Name: "gpu-1", IP: "192.0.2.10", SSHUser: "root", SSHPort: 22, SSHPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.host.SSHPasswordEnc == "" || store.host.SSHPasswordEnc == "secret" {
		t.Fatalf("password was not encrypted: %q", store.host.SSHPasswordEnc)
	}
	if !view.HasPassword || view.SSHPasswordEnc != "" || view.HostKeyFingerprint != "" {
		t.Fatalf("secret leaked in view: %+v", view)
	}
	if validator.calls != 1 || validator.password != "secret" || store.host.HostKeyFingerprint != "SHA256:test" || store.host.Arch != "amd64" || store.host.LastValidationAt == nil {
		t.Fatalf("validation was not persisted: validator=%+v host=%+v", validator, store.host)
	}
}

func TestHostUpdateConnectionValidatesBeforeSaving(t *testing.T) {
	cipher := testHostCipher(t)
	encrypted, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	previousValidation := time.Now().Add(-time.Hour)
	store := &hostStoreStub{host: domain.Host{ID: "host-1", OwnerID: "owner", Name: "old", IP: "192.0.2.10", SSHUser: "root", SSHPort: 22, SSHPasswordEnc: encrypted, Arch: "x86_64", HostKeyFingerprint: "old-fp", LastValidationAt: &previousValidation}}
	validator := &hostValidatorStub{result: remote.ValidationResult{Fingerprint: "new-fp", Arch: "arm64"}}
	_, err = NewHostService(store, cipher, validator).Update(context.Background(), "host-1", domain.HostInput{Name: "new", IP: "192.0.2.11", SSHUser: "root", SSHPort: 22})
	if err != nil {
		t.Fatal(err)
	}
	if validator.calls != 1 || validator.password != "secret" || validator.host.HostKeyFingerprint != "" {
		t.Fatalf("new target was not validated correctly: %+v", validator)
	}
	if store.host.Arch != "arm64" || store.host.HostKeyFingerprint != "new-fp" || store.host.LastValidationAt == nil || !store.host.LastValidationAt.After(previousValidation) {
		t.Fatalf("validation was not refreshed: %+v", store.host)
	}
	if store.host.SSHPasswordEnc != encrypted {
		t.Fatal("blank password replaced the stored password")
	}
}

func TestHostCreateValidationFailureDoesNotPersist(t *testing.T) {
	store := &hostStoreStub{}
	validator := &hostValidatorStub{
		result: remote.ValidationResult{Stages: []remote.ValidationStage{{Name: "connect", Message: "SSH 认证失败"}}},
		err:    errors.New("handshake failed"),
	}
	_, err := NewHostService(store, testHostCipher(t), validator).Create(context.Background(), "owner", domain.HostInput{
		Name: "gpu-1", IP: "192.0.2.10", SSHUser: "root", SSHPort: 22, SSHPassword: "wrong",
	})
	var appErr *domain.AppError
	if !errors.As(err, &appErr) || appErr.Code != "HOST_VALIDATION_FAILED" || appErr.Message != "SSH 认证失败" || store.created != 0 {
		t.Fatalf("unexpected result: err=%v created=%d", err, store.created)
	}
}

func TestHostUpdateValidationFailurePreservesStoredHost(t *testing.T) {
	cipher := testHostCipher(t)
	encrypted, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	original := domain.Host{ID: "host-1", OwnerID: "owner", Name: "gpu-old", IP: "192.0.2.10", SSHUser: "root", SSHPort: 22, SSHPasswordEnc: encrypted, HostKeyFingerprint: "old-fp", Arch: "amd64"}
	store := &hostStoreStub{host: original}
	validator := &hostValidatorStub{
		result: remote.ValidationResult{Stages: []remote.ValidationStage{{Name: "connect", Message: "SSH 认证失败"}}},
		err:    errors.New("handshake failed"),
	}
	_, err = NewHostService(store, cipher, validator).Update(context.Background(), "host-1", domain.HostInput{
		Name: "gpu-new", IP: "192.0.2.11", SSHUser: "root", SSHPort: 22,
	})
	var appErr *domain.AppError
	if !errors.As(err, &appErr) || appErr.Code != "HOST_VALIDATION_FAILED" || store.updated != 0 || store.host != original {
		t.Fatalf("failed update changed stored host: err=%v updated=%d host=%+v", err, store.updated, store.host)
	}
}

func TestHostDeleteRejectsDependencies(t *testing.T) {
	for name, test := range map[string]struct {
		store *hostStoreStub
		code  string
	}{
		"services": {&hostStoreStub{host: domain.Host{ID: "host-1"}, hasServices: true}, "HOST_HAS_SERVICES"},
		"models":   {&hostStoreStub{host: domain.Host{ID: "host-1"}, hasModels: true}, "HOST_HAS_MODELS"},
	} {
		t.Run(name, func(t *testing.T) {
			err := NewHostService(test.store, testHostCipher(t), nil).Delete(context.Background(), "host-1")
			var appErr *domain.AppError
			if !errors.As(err, &appErr) || appErr.Code != test.code || test.store.deleted {
				t.Fatalf("unexpected result: err=%v deleted=%v", err, test.store.deleted)
			}
		})
	}
}

func TestHostExportImportPreservesEncryptedCredential(t *testing.T) {
	cipher := testHostCipher(t)
	encrypted, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	source := &hostStoreStub{host: domain.Host{ID: "host-1", OwnerID: "owner", Name: "gpu-1", IP: "192.0.2.10", SSHUser: "root", SSHPort: 22, SSHPasswordEnc: encrypted}}
	doc, err := NewHostService(source, cipher, nil).Export(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	target := &hostStoreStub{created: 1}
	result, err := NewHostService(target, cipher, nil).Import(context.Background(), "owner", doc)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.Total != 1 || target.host.SSHPasswordEnc != encrypted {
		t.Fatalf("unexpected import: result=%+v host=%+v", result, target.host)
	}
}
