package postgres

import "testing"

func TestLoadMigrations(t *testing.T) {
	t.Parallel()
	items, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 || items[0].version != 1 || items[1].version != 2 ||
		items[2].version != 3 || items[3].version != 4 || items[4].version != 5 ||
		items[0].checksum == "" || items[0].content == "" || items[1].checksum == "" ||
		items[1].content == "" || items[2].checksum == "" || items[2].content == "" ||
		items[3].checksum == "" || items[3].content == "" || items[4].checksum == "" ||
		items[4].content == "" {
		t.Fatalf("unexpected migrations: %+v", items)
	}
}
