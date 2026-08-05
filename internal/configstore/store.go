package configstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrConflict = errors.New("configuration version conflict")
	ErrNotFound = errors.New("configuration not found")
)

type Validator interface {
	Validate(context.Context, string) error
}

type ValidatorFunc func(context.Context, string) error

func (fn ValidatorFunc) Validate(ctx context.Context, path string) error {
	return fn(ctx, path)
}

type Document struct {
	Content []byte
	Version string
}

type Store struct {
	mu        sync.Mutex
	directory string
	path      string
	validator Validator
}

func (s *Store) Path() string {
	return s.path
}

func Open(directory string, validator Validator) (*Store, error) {
	if validator == nil {
		return nil, fmt.Errorf("validator is required")
	}
	if err := ensurePrivateDirectory(directory); err != nil {
		return nil, err
	}
	return &Store{
		directory: directory,
		path:      filepath.Join(directory, "config.json"),
		validator: validator,
	}, nil
}

func (s *Store) Read() (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readDocument(s.path)
}

func (s *Store) Save(ctx context.Context, content []byte, expectedVersion string) (Document, error) {
	if !json.Valid(content) {
		return Document{}, fmt.Errorf("configuration is not valid JSON")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := readDocument(s.path)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Document{}, err
	}
	hadCurrent := err == nil
	if expectedVersion != "" {
		if errors.Is(err, ErrNotFound) || current.Version != expectedVersion {
			return Document{}, ErrConflict
		}
	} else if hadCurrent {
		return Document{}, ErrConflict
	}

	temporary, err := os.CreateTemp(s.directory, ".config-*.tmp")
	if err != nil {
		return Document{}, fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return Document{}, fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		return Document{}, fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return Document{}, fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Document{}, fmt.Errorf("close temporary config: %w", err)
	}

	if err := s.validator.Validate(ctx, temporaryPath); err != nil {
		return Document{}, fmt.Errorf("validate configuration: %w", err)
	}

	if hadCurrent {
		if err := createBackup(s.path, filepath.Join(s.directory, "config.json.bak")); err != nil {
			return Document{}, err
		}
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return Document{}, fmt.Errorf("commit configuration: %w", err)
	}
	committed = true

	if err := syncDirectory(s.directory); err != nil {
		return Document{}, err
	}

	return Document{Content: append([]byte(nil), content...), Version: versionOf(content)}, nil
}

func ensurePrivateDirectory(directory string) error {
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("data directory must be a real directory")
		}
		return os.Chmod(directory, 0o700)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect data directory: %w", err)
	}

	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	return os.Chmod(directory, 0o700)
}

func readDocument(path string) (Document, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("inspect configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Document{}, fmt.Errorf("configuration path is not a regular file")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read configuration: %w", err)
	}
	return Document{Content: content, Version: versionOf(content)}, nil
}

func createBackup(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open configuration for backup: %w", err)
	}
	defer input.Close()

	temporary, err := os.CreateTemp(filepath.Dir(target), ".backup-*.tmp")
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set backup permissions: %w", err)
	}
	if _, err := io.Copy(temporary, input); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync backup: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close backup: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("commit backup: %w", err)
	}
	committed = true
	return nil
}

func syncDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open data directory: %w", err)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync data directory: %w", err)
	}
	return nil
}

func versionOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
