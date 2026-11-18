package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/helpin-ai/helpin/server/internal/service"
)

func TestUnauthorizedChallengeAdvertisesScopes(t *testing.T) {
	mcpService := service.NewMCPService(
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		service.MCPServiceConfig{ServerEnabled: true, IssuerURL: "https://helpin.example"},
	)
	handler := NewHandler(mcpService, nil)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	challenge := recorder.Header().Get("WWW-Authenticate")
	wantMetadata := `resource_metadata="https://helpin.example/.well-known/oauth-protected-resource"`
	if !strings.Contains(challenge, wantMetadata) {
		t.Fatalf("challenge %q missing %s", challenge, wantMetadata)
	}
	wantScope := `scope="` + strings.Join(service.AllMCPScopes(), " ") + `"`
	if !strings.Contains(challenge, wantScope) {
		t.Fatalf("challenge %q missing %s", challenge, wantScope)
	}
}
