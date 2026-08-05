package configstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveValidatesAndDetectsConflicts(t *testing.T) {
	t.Parallel()

	store, err := Open(t.TempDir(), ValidatorFunc(func(_ context.Context, path string) error {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(content) == `{"reject":true}` {
			return errors.New("rejected by validator")
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	first, err := store.Save(context.Background(), []byte(`{"route":{}}`), "")
	if err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	if first.Version == "" {
		t.Fatal("first Save() returned an empty version")
	}

	if _, err := store.Save(context.Background(), []byte(`{"dns":{}}`), "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Save() error = %v, want ErrConflict", err)
	}
	if _, err := store.Save(context.Background(), []byte(`{"dns":{}}`), ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("blind Save() error = %v, want ErrConflict", err)
	}
	if _, err := store.Save(context.Background(), []byte(`{"reject":true}`), first.Version); err == nil {
		t.Fatal("validator rejection was not returned")
	}

	current, err := store.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(current.Content) != `{"route":{}}` {
		t.Fatalf("current content = %s, want original content", current.Content)
	}
}

func TestSaveCreatesPrivateFilesAndBackup(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	store, err := Open(directory, ValidatorFunc(func(context.Context, string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}

	first, err := store.Save(context.Background(), []byte(`{"first":true}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(context.Background(), []byte(`{"second":true}`), first.Version); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"config.json", "config.json.bak"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if permissions := info.Mode().Perm(); permissions != 0o600 {
			t.Fatalf("%s permissions = %o, want 600", name, permissions)
		}
	}
}

func TestOpenRejectsSymlinkDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(link, ValidatorFunc(func(context.Context, string) error { return nil })); err == nil {
		t.Fatal("Open() accepted a symlink data directory")
	}
}
