package wekaapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config is how to reach and log in to one Weka cluster.
type Config struct {
	// Endpoint is the cluster URL, e.g. https://weka01:14000. /api/v2 is appended if missing.
	Endpoint string
	// Org defaults to Root on the server when empty.
	Org      string
	Username string
	Password string
	// InsecureSkipVerify accepts the self-signed certificate Weka clusters ship with.
	InsecureSkipVerify bool
	// HTTPClient overrides the default client; tests use it.
	HTTPClient *http.Client
}

// API is the generated client with login and re-login handled underneath it.
type API struct {
	*ClientWithResponses
}

// New builds an API. It does not log in until the first request.
func New(cfg Config) (*API, error) {
	if cfg.Endpoint == "" || cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("weka: endpoint, username and password are required")
	}
	base := strings.TrimRight(cfg.Endpoint, "/")
	if !strings.HasSuffix(base, "/api/v2") {
		base += "/api/v2"
	}

	hc := cfg.HTTPClient
	if hc == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in, Weka ships self-signed certs
		hc = &http.Client{Timeout: 60 * time.Second, Transport: t}
	}

	login, err := NewClient(base, WithHTTPClient(hc))
	if err != nil {
		return nil, err
	}
	c, err := NewClientWithResponses(base, WithHTTPClient(&authDoer{doer: hc, login: login, cfg: cfg}))
	if err != nil {
		return nil, err
	}
	return &API{c}, nil
}

// refreshMargin re-logs in this long before the access token expires. Tokens last 5 minutes.
const refreshMargin = 30 * time.Second

// authDoer adds a bearer token to every request and re-logs in when it is close
// to expiry or the server rejects it. It re-logs in with the password rather
// than the refresh token: the provider always holds the password, so the
// refresh flow would only add a second code path.
type authDoer struct {
	doer  HttpRequestDoer
	login *Client
	cfg   Config

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (a *authDoer) Do(req *http.Request) (*http.Response, error) {
	tok, err := a.accessToken(req.Context(), "")
	if err != nil {
		return nil, err
	}
	resp, err := a.send(req, tok)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	if req.Body != nil && req.GetBody == nil {
		return resp, nil
	}
	_ = resp.Body.Close()

	tok, err = a.accessToken(req.Context(), tok)
	if err != nil {
		return nil, err
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		if retry.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	}
	return a.send(retry, tok)
}

func (a *authDoer) send(req *http.Request, tok string) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+tok)
	return a.doer.Do(r)
}

// accessToken returns a usable token. Passing the token that just got a 401
// forces a new login, unless another request already replaced it.
func (a *authDoer) accessToken(ctx context.Context, rejected string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fresh := a.token != "" && time.Until(a.expires) > refreshMargin
	if fresh && (rejected == "" || a.token != rejected) {
		return a.token, nil
	}

	body := LoginJSONRequestBody{Username: a.cfg.Username, Password: a.cfg.Password}
	if a.cfg.Org != "" {
		body.Org = &a.cfg.Org
	}
	resp, err := a.login.Login(ctx, body)
	if err != nil {
		return "", fmt.Errorf("weka login: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("weka login: %w", err)
	}
	if err := Check(resp.StatusCode, raw); err != nil {
		return "", fmt.Errorf("weka login: %w", err)
	}
	t, err := parseTokens(raw)
	if err != nil {
		return "", fmt.Errorf("weka login: %w", err)
	}

	a.token = *t.AccessToken
	ttl := 300 * time.Second
	if t.ExpiresIn != nil && *t.ExpiresIn > 0 {
		ttl = time.Duration(*t.ExpiresIn) * time.Second
	}
	a.expires = time.Now().Add(ttl)
	return a.token, nil
}

// parseTokens accepts data as an object (what the spec says) or a one-element
// array (what the docs example shows).
func parseTokens(raw []byte) (Tokens, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Tokens{}, err
	}
	var t Tokens
	if err := json.Unmarshal(env.Data, &t); err != nil {
		var list []Tokens
		if err := json.Unmarshal(env.Data, &list); err != nil || len(list) == 0 {
			return Tokens{}, fmt.Errorf("unexpected login response: %s", truncate(raw))
		}
		t = list[0]
	}
	if t.AccessToken == nil || *t.AccessToken == "" {
		return Tokens{}, errors.New("login response has no access_token")
	}
	return t, nil
}

// APIError is a non-2xx response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("weka api: HTTP %d: %s", e.Status, e.Message)
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// Check returns nil for 2xx and an *APIError otherwise.
// Use it on every generated response: resp.StatusCode(), resp.Body.
func Check(status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	return &APIError{Status: status, Message: errorMessage(body)}
}

func errorMessage(body []byte) string {
	var env struct {
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &env) == nil {
		if env.Message != "" {
			if detail := strings.TrimSpace(string(env.Data)); detail != "" && detail != "null" {
				return env.Message + ": " + detail
			}
			return env.Message
		}
		var s string
		if json.Unmarshal(env.Data, &s) == nil && s != "" {
			return s
		}
		var d struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(env.Data, &d) == nil && d.Error != "" {
			return d.Error
		}
	}
	return truncate(body)
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}

// Poll calls check every interval until it reports done, returns an error, or
// ctx ends. Use it for calls that return before the change has finished.
func Poll(ctx context.Context, interval time.Duration, check func(context.Context) (bool, error)) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		done, err := check(ctx)
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
