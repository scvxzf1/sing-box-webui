package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

type Directories struct {
	Config  string
	State   string
	Runtime string
}

func Resolve(appName string) (Directories, error) {
	if appName == "" {
		return Directories{}, fmt.Errorf("app name is required")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Directories{}, fmt.Errorf("resolve home directory: %w", err)
	}

	configRoot := envOrDefault("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	stateRoot := envOrDefault("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	runtimeRoot := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeRoot == "" {
		runtimeRoot = filepath.Join(stateRoot, appName, "run")
		return Directories{
			Config:  filepath.Join(configRoot, appName),
			State:   filepath.Join(stateRoot, appName),
			Runtime: runtimeRoot,
		}, nil
	}

	return Directories{
		Config:  filepath.Join(configRoot, appName),
		State:   filepath.Join(stateRoot, appName),
		Runtime: filepath.Join(runtimeRoot, appName),
	}, nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
