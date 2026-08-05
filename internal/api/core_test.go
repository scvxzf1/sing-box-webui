package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sing-box-webui/internal/core"
)

type fakeCoreController struct {
	info             core.Info
	requestedVersion string
}

func (controller *fakeCoreController) Info(context.Context) (core.Info, error) {
	return controller.info, nil
}

func (controller *fakeCoreController) Update(_ context.Context, version string) (core.Info, error) {
	controller.requestedVersion = version
	controller.info.PreviousVersion = controller.info.CurrentVersion
	controller.info.CurrentVersion = "2.0.0"
	return controller.info, nil
}

func (controller *fakeCoreController) Rollback(context.Context) (core.Info, error) {
	controller.info.CurrentVersion, controller.info.PreviousVersion = controller.info.PreviousVersion, controller.info.CurrentVersion
	return controller.info, nil
}

func TestCoreStatusAndUpdate(t *testing.T) {
	controller := &fakeCoreController{info: core.Info{
		Source: "managed", CurrentVersion: "1.0.0", EmbeddedVersion: "1.0.0",
		UpdateSupported: true, Platform: "linux/amd64",
	}}
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173", Core: controller,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/core", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", response.Code, response.Body.String())
	}
	var info core.Info
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.CurrentVersion != "1.0.0" {
		t.Fatalf("GET info = %+v", info)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/core/update", strings.NewReader(`{"version":"2.0.0"}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || controller.requestedVersion != "2.0.0" {
		t.Fatalf("POST status = %d, requested = %q, body = %s", response.Code, controller.requestedVersion, response.Body.String())
	}
}
