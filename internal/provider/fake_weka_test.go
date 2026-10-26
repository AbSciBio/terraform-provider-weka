package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var protoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"weka": providerserver.NewProtocol6WithError(New("test")()),
}

const fakeToken = "fake-token"

// fakeWeka is an in-memory Weka API for unit tests. It serves login itself;
// each resource test registers the handlers it needs with Handle.
type fakeWeka struct {
	*httptest.Server
	mux *http.ServeMux
}

func newFakeWeka(t *testing.T) *fakeWeka {
	t.Helper()
	f := &fakeWeka{mux: http.NewServeMux()}
	f.Handle("POST /login", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, map[string]any{"access_token": fakeToken, "expires_in": 300})
	})
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/login" && r.Header.Get("Authorization") != "Bearer "+fakeToken {
			writeError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		f.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

// Handle registers a handler for a pattern relative to /api/v2, e.g. "GET /fileSystemGroups/{uid}".
func (f *fakeWeka) Handle(pattern string, h http.HandlerFunc) {
	method, p, _ := strings.Cut(pattern, " ")
	f.mux.HandleFunc(method+" /api/v2"+p, h)
}

// config prefixes a test configuration with a provider block pointing at the fake.
func (f *fakeWeka) config(body string) string {
	return fmt.Sprintf(`
provider "weka" {
  endpoint = %q
  username = "admin"
  password = "secret"
}
%s`, f.URL, body)
}

func writeData(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": msg})
}

func decodeBody(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Errorf("decoding %s %s body: %v", r.Method, r.URL.Path, err)
	}
}
