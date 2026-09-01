package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"DP/internal/domain"
	"DP/internal/testdb"
)

func TestPackageRepositoryIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	first := packageVersionFixture("package-first", "a")
	if err := db.SavePackageVersion(ctx, first); err != nil {
		t.Fatalf("save first package version: %v", err)
	}
	second := packageVersionFixture("package-second", "b")
	if err := db.SavePackageVersion(ctx, second); err != nil {
		t.Fatalf("save second package version: %v", err)
	}
	pkg, err := db.GetPackageByOwner(ctx, domain.InitialAdminID, first.ServiceType)
	if err != nil {
		t.Fatalf("get package: %v", err)
	}
	if pkg.CurrentVersionID != second.ID || pkg.VersionCount != 2 {
		t.Fatalf("unexpected current package: %+v", pkg)
	}
	versions, err := db.ListPackageVersions(ctx, domain.InitialAdminID, first.ServiceType)
	if err != nil || len(versions) != 2 || !versions[0].Current {
		t.Fatalf("unexpected package versions: %+v err=%v", versions, err)
	}
	if err := db.DeletePackageVersion(ctx, domain.InitialAdminID, first.ServiceType, second.ID); appErrorCode(err) != "PACKAGE_VERSION_CURRENT" {
		t.Fatalf("expected current-version protection, got %v", err)
	}
	if err := db.ActivatePackageVersion(ctx, first); err != nil {
		t.Fatalf("activate first package version: %v", err)
	}
	if err := db.DeletePackageVersion(ctx, domain.InitialAdminID, first.ServiceType, second.ID); err != nil {
		t.Fatalf("delete inactive package version: %v", err)
	}
	if err := db.DeletePackageByOwner(ctx, domain.InitialAdminID, first.ServiceType); err != nil {
		t.Fatalf("delete package: %v", err)
	}
}

func packageVersionFixture(id, shaByte string) domain.PackageVersion {
	return domain.PackageVersion{
		ID: domain.NewID(), OwnerID: domain.InitialAdminID, ServiceType: "package-integration",
		OriginalFilename: id + ".tar.gz", StoragePath: "/tmp/" + id + ".tar.gz",
		SHA256: strings.Repeat(shaByte, 64), SizeBytes: 1024, ConfigPort: 18080,
		ConfigFormat: "yaml", ConfigPath: "config.yaml", ConfigContent: []byte("port: 18080\n"),
		UploadedAt: time.Now().UTC(),
	}
}

func appErrorCode(err error) string {
	if appErr, ok := err.(*domain.AppError); ok {
		return appErr.Code
	}
	return ""
}
