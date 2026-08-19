package application

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultAddress   = "127.0.0.1:33334"
	DefaultDevOrigin = "http://127.0.0.1:33333"
	DefaultMixedPort = 2080
)

type Config struct {
	Address        string
	DevOrigin      string
	DataDir        string
	SingBoxBinary  string
	EnableTUN      bool
	MixedPort      uint16
	WebAuthEnabled bool
	WebToken       string
	ConfigPath     string
	DohEndpoint    string
}

type projectConfig struct {
	Web struct {
		Enabled *bool  `json:"enabled,omitempty"`
		Token   string `json:"token,omitempty"`
	} `json:"web"`
}

func LoadConfig() (Config, error) {
	config := Config{
		Address:       envOrDefault("SING_BOX_WEBUI_ADDR", DefaultAddress),
		DevOrigin:     envOrDefault("SING_BOX_WEBUI_DEV_ORIGIN", DefaultDevOrigin),
		DohEndpoint:   envOrDefault("SING_BOX_WEBUI_DOH_ENDPOINT", "https://1.12.12.12/dns-query"),
		SingBoxBinary: strings.TrimSpace(os.Getenv("SING_BOX_BIN")),
		EnableTUN:     parseEnvBool("SING_BOX_WEBUI_ENABLE_TUN"),
		MixedPort:     DefaultMixedPort,
	}

	if err := ValidateLoopbackAddress(config.Address); err != nil {
		return Config{}, fmt.Errorf("SING_BOX_WEBUI_ADDR: %w", err)
	}
	dataDirectory := envOrDefault("SING_BOX_WEBUI_DATA_DIR", "./var/data")
	absoluteDataDirectory, err := filepath.Abs(dataDirectory)
	if err != nil {
		return Config{}, fmt.Errorf("SING_BOX_WEBUI_DATA_DIR: %w", err)
	}
	config.DataDir = absoluteDataDirectory
	configPath := envOrDefault("SING_BOX_WEBUI_CONFIG", "./var/config.json")
	absoluteConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("SING_BOX_WEBUI_CONFIG: %w", err)
	}
	config.ConfigPath = absoluteConfigPath
	config.WebToken, config.WebAuthEnabled, err = loadOrCreateWebConfig(absoluteConfigPath)
	if err != nil {
		return Config{}, fmt.Errorf("load project config: %w", err)
	}
	if portValue := strings.TrimSpace(os.Getenv("SING_BOX_WEBUI_MIXED_PORT")); portValue != "" {
		port, err := strconv.ParseUint(portValue, 10, 16)
		if err != nil || port == 0 {
			return Config{}, fmt.Errorf("SING_BOX_WEBUI_MIXED_PORT must be between 1 and 65535")
		}
		config.MixedPort = uint16(port)
	}

	return config, nil
}

func loadOrCreateWebConfig(path string) (string, bool, error) {
	contents, err := os.ReadFile(path)
	if err == nil {
		if err := os.Chmod(path, 0o600); err != nil {
			return "", false, fmt.Errorf("secure %s: %w", path, err)
		}
		var stored projectConfig
		if err := json.Unmarshal(contents, &stored); err != nil {
			return "", false, fmt.Errorf("parse %s: %w", path, err)
		}
		enabled := stored.Web.Enabled == nil || *stored.Web.Enabled
		if !enabled {
			return "", false, nil
		}
		token := strings.TrimSpace(stored.Web.Token)
		if len(token) < 8 {
			return "", false, fmt.Errorf("web.token in %s must contain at least 8 characters", path)
		}
		return token, true, nil
	}
	if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}

	var tokenBytes [32]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return "", false, fmt.Errorf("generate web token: %w", err)
	}
	var stored projectConfig
	enabled := true
	stored.Web.Enabled = &enabled
	stored.Web.Token = base64.RawURLEncoding.EncodeToString(tokenBytes[:])
	contents, err = json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return "", false, fmt.Errorf("encode project config: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, fmt.Errorf("create config directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", false, fmt.Errorf("create %s: %w", path, err)
	}
	if _, writeErr := file.Write(contents); writeErr != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", false, fmt.Errorf("write %s: %w", path, writeErr)
	}
	if err := file.Close(); err != nil {
		return "", false, fmt.Errorf("close %s: %w", path, err)
	}
	return stored.Web.Token, true, nil
}

func parseEnvBool(key string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	result, _ := strconv.ParseBool(value)
	return result || value == "1"
}

func ValidateLoopbackAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid host:port: %w", err)
	}

	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("host %q is not an explicit loopback IP", host)
	}

	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("port %q is outside 1-65535", portText)
	}

	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
