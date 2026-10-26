package wekaapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeLogin serves /api/v2/login and counts logins. Tokens are tok-1, tok-2, ...
func fakeLogin(t *testing.T, mux *http.ServeMux, expiresIn int, asArray bool) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	mux.HandleFunc("POST /api/v2/login", func(w http.ResponseWriter, r *http.Request) {
		var body LoginJSONBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username != "u" || body.Password != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"data":"Unauthorized"}`))
			return
		}
		tok := map[string]any{"access_token": fmt.Sprintf("tok-%d", n.Add(1)), "expires_in": expiresIn}
		var data any = tok
		if asArray {
			data = []any{tok}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	return &n
}

func newAPI(t *testing.T, srv *httptest.Server, password string) *API {
	t.Helper()
	api, err := New(Config{Endpoint: srv.URL, Username: "u", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func TestLoginOnceAndReuseToken(t *testing.T) {
	for _, asArray := range []bool{false, true} {
		mux := http.NewServeMux()
		logins := fakeLogin(t, mux, 300, asArray)
		mux.HandleFunc("GET /api/v2/fileSystemGroups", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok-1" {
				t.Errorf("auth header = %q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":[]}`))
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		api := newAPI(t, srv, "p")

		for range 3 {
			resp, err := api.GetFileSystemGroupsWithResponse(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := Check(resp.StatusCode(), resp.Body); err != nil {
				t.Fatal(err)
			}
		}
		if got := logins.Load(); got != 1 {
			t.Errorf("asArray=%v: logins = %d, want 1", asArray, got)
		}
	}
}

func TestReloginNearExpiry(t *testing.T) {
	mux := http.NewServeMux()
	logins := fakeLogin(t, mux, 1, false)
	mux.HandleFunc("GET /api/v2/fileSystemGroups", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	api := newAPI(t, srv, "p")

	for range 2 {
		if _, err := api.GetFileSystemGroupsWithResponse(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := logins.Load(); got != 2 {
		t.Errorf("logins = %d, want 2 (token inside refresh margin)", got)
	}
}

func TestReloginOn401ReplaysBody(t *testing.T) {
	mux := http.NewServeMux()
	logins := fakeLogin(t, mux, 300, false)
	var bodies []string
	mux.HandleFunc("POST /api/v2/fileSystemGroups", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if r.Header.Get("Authorization") == "Bearer tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"data":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"uid":"g1","name":"a"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	api := newAPI(t, srv, "p")

	resp, err := api.CreateFileSystemGroupWithResponse(context.Background(), CreateFileSystemGroupJSONRequestBody{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(resp.StatusCode(), resp.Body); err != nil {
		t.Fatal(err)
	}
	if got := logins.Load(); got != 2 {
		t.Errorf("logins = %d, want 2", got)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[1] == "" {
		t.Errorf("retry body not replayed: %q", bodies)
	}
}

func TestLoginFailureIsAPIError(t *testing.T) {
	mux := http.NewServeMux()
	fakeLogin(t, mux, 300, false)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	api := newAPI(t, srv, "wrong")

	_, err := api.GetFileSystemGroupsWithResponse(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Message != "Unauthorized" {
		t.Errorf("err = %v", err)
	}
}

func TestCheckMessages(t *testing.T) {
	cases := map[string]string{
		`{"message":"not found"}`:                  "not found",
		`{"data":"Unauthorized"}`:                  "Unauthorized",
		`{"data":{"error":"param2 has an error"}}`: "param2 has an error",
		`plain text`:                               "plain text",
	}
	for body, want := range cases {
		err := Check(400, []byte(body))
		if e, ok := err.(*APIError); !ok || e.Message != want {
			t.Errorf("Check(%s) = %v, want message %q", body, err, want)
		}
	}
	if Check(204, nil) != nil {
		t.Error("2xx should be nil")
	}
	if !IsNotFound(Check(404, []byte(`{"message":"x"}`))) {
		t.Error("IsNotFound(404) = false")
	}
}

func TestPoll(t *testing.T) {
	n := 0
	err := Poll(context.Background(), time.Millisecond, func(context.Context) (bool, error) {
		n++
		return n == 3, nil
	})
	if err != nil || n != 3 {
		t.Errorf("Poll: n=%d err=%v", n, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = Poll(ctx, time.Millisecond, func(context.Context) (bool, error) { return false, nil })
	if err != context.DeadlineExceeded {
		t.Errorf("Poll timeout err = %v", err)
	}
}
