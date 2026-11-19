package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A damaged database (here cut short, as one written when the daemon was
// killed) is kept aside, and the store starts with a fresh one.
func TestOpenSetsADamagedDatabaseAside(t *testing.T) {
	dirs := testDirs(t)
	store, err := Open(dirs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("CREATE TABLE filler (body TEXT)"); err != nil {
		t.Fatal(err)
	}
	for range 400 {
		if _, err := store.db.Exec("INSERT INTO filler VALUES (?)", strings.Repeat("value", 200)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	store.Close()

	path := filepath.Join(dirs.Data, databaseName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)/3], 0o600); err != nil {
		t.Fatal(err)
	}

	store, err = Open(dirs, nil)
	if err != nil {
		t.Fatalf("a damaged database kept the store from opening: %v", err)
	}
	defer store.Close()
	if _, err := store.db.Exec("SELECT count(*) FROM settings"); err != nil {
		t.Fatal(err)
	}
	kept, _ := filepath.Glob(path + ".damaged-*")
	if len(kept) == 0 {
		t.Fatal("the damaged database was not kept")
	}
}
