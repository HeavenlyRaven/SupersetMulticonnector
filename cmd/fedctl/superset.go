package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/acme/superset-federation/internal/config"
)

// supersetClient is a minimal client for Superset's core REST API (stable
// since well before the pre-1.0 extensions framework) — just enough to
// log in, fetch a CSRF token, and idempotently register the ClickHouse
// connection per spec section 12. It is deliberately not a general
// Superset API client.
type supersetClient struct {
	baseURL     string
	http        *http.Client
	accessToken string
	csrfToken   string
	cookie      string
}

func newSupersetClient(baseURL string) *supersetClient {
	return &supersetClient{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *supersetClient) login(ctx context.Context, username, password string) error {
	body, _ := json.Marshal(map[string]any{
		"username": username, "password": password, "provider": "db", "refresh": true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/security/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("superset login request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("superset login returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return fmt.Errorf("parsing superset login response: %w", err)
	}
	c.accessToken = out.AccessToken
	return nil
}

func (c *supersetClient) fetchCSRF(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/security/csrf_token/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("superset csrf request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("superset csrf endpoint returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "session" {
			c.cookie = ck.String()
		}
	}
	var out struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("parsing superset csrf response: %w", err)
	}
	c.csrfToken = out.Result
	return nil
}

// createDatabaseIdempotent posts payload to /api/v1/database/, treating a
// name-uniqueness conflict as success — Superset has no "upsert" endpoint,
// so this is how `fedctl up` stays idempotent across repeated runs.
func (c *supersetClient) createDatabaseIdempotent(ctx context.Context, payload any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/database/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("X-CSRFToken", c.csrfToken)
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("creating database connection failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return nil
	}
	if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(string(respBody)), "already exists") {
		return nil // idempotent: already registered by a prior `fedctl up`
	}
	return fmt.Errorf("creating database connection returned HTTP %d: %s", resp.StatusCode, string(respBody))
}

type createDatabasePayload struct {
	DatabaseName  string `json:"database_name"`
	SQLAlchemyURI string `json:"sqlalchemy_uri"`
	Expose        bool   `json:"expose_in_sqllab"`
	AllowDML      bool   `json:"allow_dml"`
	AllowCTAS     bool   `json:"allow_ctas"`
	AllowCVAS     bool   `json:"allow_cvas"`
}

// bootstrapSuperset runs Superset's one-time metadata schema migration,
// admin user creation, and default-role init via `docker compose exec` —
// standard, long-stable `superset` CLI subcommands, not a script we wrote.
// The stack is exactly two containers (spec section 1), so there is no
// separate init container to do this instead.
//
// superset fab create-admin has no stdin-based credential input, so its
// password necessarily travels as a CLI flag here — that argv is only
// ever visible on the host running `fedctl up` for the lifetime of this
// one `docker compose exec` call, inside the Superset container's own
// process table, not fedctl's. This is intentionally distinct from
// fedctl's own credential-flag rule in internal/validate, which governs
// fedctl's own argv, not a third-party CLI's. See the README's known
// limitations section.
func bootstrapSuperset(ctx context.Context, dev bool, app config.App) error {
	if err := runDockerCompose(ctx, runComposeArgs(dev, "exec", "-T", "superset", "superset", "db", "upgrade")); err != nil {
		return fmt.Errorf("superset db upgrade failed: %w", err)
	}
	if app.SupersetAdminUser != "" && app.SupersetAdminPass != "" {
		createArgs := runComposeArgs(dev, "exec", "-T", "superset", "superset", "fab", "create-admin",
			"--username", app.SupersetAdminUser,
			"--firstname", "Superset",
			"--lastname", "Admin",
			"--email", app.SupersetAdminUser+"@superset.local",
			"--password", app.SupersetAdminPass,
		)
		// Not fatal: the common failure here is "user already exists",
		// which just means a prior `fedctl up` already did this step.
		_ = runDockerCompose(ctx, createArgs)
	}
	if err := runDockerCompose(ctx, runComposeArgs(dev, "exec", "-T", "superset", "superset", "init")); err != nil {
		return fmt.Errorf("superset init failed: %w", err)
	}
	return nil
}

// registerClickHouseConnection implements spec section 12: idempotently
// register clickhousedb://bi_ro:***@clickhouse:8123/default in Superset,
// with SQL Lab exposure on and DML/CTAS/CVAS off (bi_ro is readonly=2 on
// the ClickHouse side too — this is belt and braces).
func registerClickHouseConnection(ctx context.Context, app config.App) error {
	if app.SupersetAdminUser == "" || app.SupersetAdminPass == "" {
		return fmt.Errorf("SUPERSET_ADMIN_USER and SUPERSET_ADMIN_PASSWORD must be set to auto-register the ClickHouse connection")
	}
	client := newSupersetClient(app.SupersetBaseURL)
	if err := client.login(ctx, app.SupersetAdminUser, app.SupersetAdminPass); err != nil {
		return err
	}
	if err := client.fetchCSRF(ctx); err != nil {
		return err
	}
	uri := fmt.Sprintf("clickhousedb://%s:%s@%s:8123/default",
		url.QueryEscape(app.BIReadOnlyUser), url.QueryEscape(app.BIReadOnlyPassword), app.ClickHouseHost)
	payload := createDatabasePayload{
		DatabaseName:  "ClickHouse Federation Hub",
		SQLAlchemyURI: uri,
		Expose:        true,
		AllowDML:      false,
		AllowCTAS:     false,
		AllowCVAS:     false,
	}
	return client.createDatabaseIdempotent(ctx, payload)
}
