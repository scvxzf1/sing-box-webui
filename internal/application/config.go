package application

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultAddress   = "127.0.0.1:11872"
	DefaultDevOrigin = "http://127.0.0.1:5173"
	DefaultMixedPort = 2080
)

type Config struct {
	Address       string
	DevOrigin     string
	DataDir       string
	SingBoxBinary string
	EnableTUN     bool
	MixedPort     uint16
}

func LoadConfig() (Config, error) {
	config := Config{
		Address:       envOrDefault("SING_BOX_WEBUI_ADDR", DefaultAddress),
		DevOrigin:     envOrDefault("SING_BOX_WEBUI_DEV_ORIGIN", DefaultDevOrigin),
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
	if portValue := strings.TrimSpace(os.Getenv("SING_BOX_WEBUI_MIXED_PORT")); portValue != "" {
		port, err := strconv.ParseUint(portValue, 10, 16)
		if err != nil || port == 0 {
			return Config{}, fmt.Errorf("SING_BOX_WEBUI_MIXED_PORT must be between 1 and 65535")
		}
		config.MixedPort = uint16(port)
	}

	return config, nil
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
