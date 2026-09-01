package postgres

import "testing"

func TestLoadMigrations(t *testing.T) {
	t.Parallel()
	items, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].version != 1 || items[0].checksum == "" || items[0].content == "" {
		t.Fatalf("unexpected migrations: %+v", items)
	}
}
