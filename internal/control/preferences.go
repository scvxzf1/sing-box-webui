package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type runtimePreferences struct {
	AllowLan bool `json:"allowLan"`
}

func loadRuntimePreferences(path string) (runtimePreferences, error) {
	if path == "" {
		return runtimePreferences{}, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return runtimePreferences{}, nil
	}
	if err != nil {
		return runtimePreferences{}, fmt.Errorf("inspect runtime preferences: %w", err)
	}
	if !info.Mode().IsRegular() {
		return runtimePreferences{}, fmt.Errorf("runtime preferences path is not a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return runtimePreferences{}, fmt.Errorf("read runtime preferences: %w", err)
	}
	var preferences runtimePreferences
	if err := json.Unmarshal(content, &preferences); err != nil {
		return runtimePreferences{}, fmt.Errorf("parse runtime preferences: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return runtimePreferences{}, fmt.Errorf("secure runtime preferences: %w", err)
	}
	return preferences, nil
}

func saveRuntimePreferences(path string, preferences runtimePreferences) error {
	if path == "" {
		return nil
	}
	content, err := json.MarshalIndent(preferences, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime preferences: %w", err)
	}
	content = append(content, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create runtime preferences directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure runtime preferences directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".preferences-*.tmp")
	if err != nil {
		return fmt.Errorf("create runtime preferences: %w", err)
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
		return fmt.Errorf("secure temporary runtime preferences: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		return fmt.Errorf("write runtime preferences: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync runtime preferences: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close runtime preferences: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit runtime preferences: %w", err)
	}
	committed = true
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open runtime preferences directory: %w", err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return fmt.Errorf("sync runtime preferences directory: %w", err)
	}
	return nil
}
