package desktop

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetadataRejectsOldSchemasWithoutChangingThem(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4, 99} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "metadata.db")
			db, err := openMetadata(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Execute(context.Background(), fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
				t.Fatal(err)
			}
			db.Close()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			db, err = openMetadata(path)
			if err == nil {
				db.Close()
				t.Fatal("unsupported schema was accepted")
			}
			if !strings.Contains(err.Error(), "--reset-metadata") {
				t.Fatalf("no reset instruction: %v", err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(before, after) {
				t.Fatal("opening obsolete metadata changed its contents")
			}
		})
	}
}
func TestMetadataResetOnlyRemovesItsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.db")
	for _, name := range []string{"metadata.db", "metadata.db-wal", "metadata.db-shm", "opencode.db", "other.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep outside app metadata"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := resetMetadata(path); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"metadata.db", "metadata.db-wal", "metadata.db-shm"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived reset: %v", name, err)
		}
	}
	for _, name := range []string{"opencode.db", "other.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	db, err := openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
}
func TestCorruptMetadataIsNeverAutomaticallyReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	raw := []byte("this is not SQLite")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := openMetadata(path)
	if err == nil {
		db.Close()
		t.Fatal("corrupt metadata opened")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("corrupt data was silently changed")
	}
}
