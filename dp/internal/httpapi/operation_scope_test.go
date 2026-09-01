package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"DP/internal/operation"
	"DP/internal/testutil"
)

func TestOperationOwnScopeIncludesOwnedAndActorOperationsOnly(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	first := mustCreateOperationUser(t, ctx, db, "operation-first")
	second := mustCreateOperationUser(t, ctx, db, "operation-second")
	third := mustCreateOperationUser(t, ctx, db, "operation-third")
	firstEnv := mustCreateOperationEnvironment(t, ctx, db, first.ID, "192.0.2.31")
	secondEnv := mustCreateOperationEnvironment(t, ctx, db, second.ID, "192.0.2.32")

	owned := mustCreateOperation(t, ctx, db, firstEnv.ID, first.ID, first.ID)
	acted := mustCreateOperation(t, ctx, db, secondEnv.ID, first.ID, second.ID)
	hidden := mustCreateOperation(t, ctx, db, secondEnv.ID, third.ID, second.ID)

	api := &API{store: db, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	first.Permissions = access.Grants{access.OperationRead: access.ScopeOwn}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/operations?owner_id="+second.ID, nil)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: first}))
	recorder := httptest.NewRecorder()
	api.listOperations(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Items []domain.Operation `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(response.Data.Items))
	for _, item := range response.Data.Items {
		got[item.ID] = true
	}
	if !got[owned.ID] || !got[acted.ID] || got[hidden.ID] || len(got) != 2 {
		t.Fatalf("visible operation IDs = %v", got)
	}

	manager := operation.NewManager(ctx, t.TempDir(), db, nil, nil, nil, nil, slog.Default())
	api.operations = manager
	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/operations/"+acted.ID, nil)
	detailRequest = detailRequest.WithContext(context.WithValue(detailRequest.Context(), authContextKey{}, authenticated{User: first}))
	if _, err := api.authorizeOperation(detailRequest, acted.ID); err != nil {
		t.Fatalf("actor cannot read own operation: %v", err)
	}
	third.Permissions = access.Grants{access.OperationRead: access.ScopeOwn}
	detailRequest = detailRequest.WithContext(context.WithValue(detailRequest.Context(), authContextKey{}, authenticated{User: third}))
	if _, err := api.authorizeOperation(detailRequest, acted.ID); err != domain.ErrForbidden {
		t.Fatalf("unrelated operation access error = %v", err)
	}
}

func mustCreateOperationUser(t *testing.T, ctx context.Context, db operationTestRepository, username string) domain.User {
	t.Helper()
	user, err := db.CreateUser(ctx, testutil.User(t, username, access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	return user
}

type operationTestRepository interface {
	CreateUser(context.Context, domain.User) (domain.User, error)
	CreateEnvironment(context.Context, domain.Environment) (domain.Environment, error)
	CreateOperation(context.Context, domain.Operation) error
}

func mustCreateOperationEnvironment(
	t *testing.T,
	ctx context.Context,
	db operationTestRepository,
	ownerID string,
	ip string,
) domain.Environment {
	t.Helper()
	environment, err := db.CreateEnvironment(ctx, domain.Environment{
		OwnerID: ownerID, Name: "operation-env-" + ip, IP: ip, SSHUser: "dp", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/dp", ServiceType: "demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	return environment
}

func mustCreateOperation(
	t *testing.T,
	ctx context.Context,
	db operationTestRepository,
	environmentID string,
	actorID string,
	ownerID string,
) domain.Operation {
	t.Helper()
	item := domain.Operation{
		ID: domain.NewID(), EnvironmentID: environmentID, ActorUserID: actorID, OwnerID: ownerID,
		Action: domain.ActionStart, Status: domain.OperationSucceeded, Stage: "done",
		LogPath: "operations/test.jsonl", CreatedAt: time.Now().UTC(),
	}
	if err := db.CreateOperation(ctx, item); err != nil {
		t.Fatal(err)
	}
	return item
}
