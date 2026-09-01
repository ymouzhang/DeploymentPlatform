package postgres

import "testing"

func TestLoadMigrations(t *testing.T) {
	t.Parallel()
	items, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 7 {
		t.Fatalf("migration count = %d, want 7", len(items))
	}
	for index, item := range items {
		if item.version != index+1 || item.checksum == "" || item.content == "" {
			t.Fatalf("migration[%d] = %+v", index, item)
		}
	}
}
