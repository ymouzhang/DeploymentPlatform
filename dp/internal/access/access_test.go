package access

import "testing"

func TestDefinitionsAreUniqueAndValid(t *testing.T) {
	t.Parallel()
	seenKeys := make(map[Permission]struct{})
	seenActions := make(map[string]struct{})
	for _, definition := range Definitions() {
		if definition.Key == "" || definition.Resource == "" || definition.Action == "" || definition.Description == "" {
			t.Fatalf("incomplete definition: %+v", definition)
		}
		if _, exists := seenKeys[definition.Key]; exists {
			t.Fatalf("duplicate permission key %q", definition.Key)
		}
		seenKeys[definition.Key] = struct{}{}
		action := definition.Resource + "." + definition.Action
		if _, exists := seenActions[action]; exists {
			t.Fatalf("duplicate resource action %q", action)
		}
		seenActions[action] = struct{}{}
	}
}

func TestMergeUsesStrongestScope(t *testing.T) {
	t.Parallel()
	grants, err := Merge(
		Grant{Permission: ServiceRead, Scope: ScopeOwn},
		Grant{Permission: ServiceRead, Scope: ScopeAll},
		Grant{Permission: PackageRead, Scope: ScopeOwn},
	)
	if err != nil {
		t.Fatal(err)
	}
	if scope, ok := grants.Scope(ServiceRead); !ok || scope != ScopeAll {
		t.Fatalf("service scope = %q, %v", scope, ok)
	}
	if !grants.Allows(ServiceRead, "user-a", "user-b") {
		t.Fatal("all scope should allow another owner")
	}
	if grants.Allows(PackageRead, "user-a", "user-b") {
		t.Fatal("own scope should reject another owner")
	}
	if !grants.Allows(PackageRead, "user-a", "user-a") {
		t.Fatal("own scope should allow the subject")
	}
}

func TestMergeRejectsInvalidScope(t *testing.T) {
	t.Parallel()
	if _, err := Merge(Grant{Permission: ServiceRead, Scope: "team"}); err == nil {
		t.Fatal("expected invalid scope error")
	}
}

func TestCanGrantCannotEscalateOwnToAll(t *testing.T) {
	t.Parallel()
	grants := Grants{ServiceRead: ScopeOwn, PackageRead: ScopeAll}
	if grants.CanGrant(ServiceRead, ScopeAll) {
		t.Fatal("own grant must not grant all")
	}
	if !grants.CanGrant(ServiceRead, ScopeOwn) || !grants.CanGrant(PackageRead, ScopeOwn) {
		t.Fatal("existing scope should allow equal or narrower grants")
	}
}
