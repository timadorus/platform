package e2eutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SeedRulesetName is the fixed Ruleset name devcluster seeds into a fresh platform — a literal
// per this tool's own requirement, exported so up.go's printStatus can reference the same
// constant seed.go seeds with instead of duplicating the string.
const SeedRulesetName = "Timadorus"

// seedResource is the minimal shape shared by every query-api list-of-summaries response
// (/users, /rulesets, ...) — id/name, same fields the web SPA's own UserSummary/RulesetSummary
// types carry.
type seedResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SeedPlatformData creates baseline dev data through the platform's own HTTP APIs, reached via a
// temporary port-forward to the same shared Traefik Gateway a developer's browser uses (a live
// smoke test of that routing as a side effect): a User matching zitadel.TestLoginName (i.e.
// "devuser@timadorus.local" — derived from the same value the login form accepts, not
// re-hardcoded, so it can never drift from the account a developer actually logs in with) and a
// Ruleset named SeedRulesetName. Each is checked against the query-api's own list by name first
// and skipped if already present — User/Ruleset names carry no uniqueness constraint at the
// domain level, so an unconditional create would pile up duplicates on every repeated
// `make dev-up` against an already-seeded cluster. zitadelPort/gatewayPort are reused transiently
// (matching InstallZitadel's own reuse of externalPort for its bootstrap port-forward) — nothing
// else holds either port open during `up` itself.
func SeedPlatformData(zitadel ZitadelBootstrap, zitadelPort, gatewayPort int) error {
	token, err := fetchSeedAccessToken(zitadel, zitadelPort)
	if err != nil {
		return fmt.Errorf("e2eutil: fetch seed access token: %w", err)
	}

	prevNamespace := Namespace
	Namespace = TraefikNamespace
	pf, err := StartPortForward("traefik", gatewayPort, 80, "/", 2*time.Minute)
	Namespace = prevNamespace
	if err != nil {
		return fmt.Errorf("e2eutil: port-forward traefik for seeding: %w", err)
	}
	defer pf.Stop()

	// "localhost", not "127.0.0.1": Traefik's HTTPRoute is host-matched against
	// gateway.pathRouting.hostname (up.go sets it to "localhost" — see PathRoutingHostname),
	// same as Zitadel's ExternalDomain check below — reproduced live as a 404 from Traefik
	// itself (no matching route) when this used 127.0.0.1.
	base := fmt.Sprintf("http://localhost:%d", gatewayPort)
	if err := ensureSeedResource(base, token, "/api/query/users", "/api/command/users", zitadel.TestLoginName); err != nil {
		return fmt.Errorf("e2eutil: seed user: %w", err)
	}
	if err := ensureSeedResource(base, token, "/api/query/rulesets", "/api/command/rulesets", SeedRulesetName); err != nil {
		return fmt.Errorf("e2eutil: seed ruleset: %w", err)
	}
	return nil
}

// fetchSeedAccessToken fetches a client_credentials access token for the FirstInstance-bootstrapped
// API machine user, via a temporary port-forward to Zitadel's own Service — the identical request
// printStatus's own "Direct API access" curl example documents as working.
func fetchSeedAccessToken(zitadel ZitadelBootstrap, zitadelPort int) (string, error) {
	prevNamespace := Namespace
	Namespace = ZitadelNamespace
	pf, err := StartPortForward(ZitadelServiceName, zitadelPort, 8080, "/.well-known/openid-configuration", 2*time.Minute)
	Namespace = prevNamespace
	if err != nil {
		return "", fmt.Errorf("port-forward zitadel: %w", err)
	}
	defer pf.Stop()

	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"openid profile"},
	}
	// "localhost", not "127.0.0.1": Zitadel's ExternalDomain=localhost (see
	// installZitadelHelmRelease) makes it validate the request's Host header against that
	// domain — reproduced live as "HTTP 404: unable to set instance using origin
	// &{127.0.0.1:<port> ... }: ID=QUERY-1kIjX Message=Instance not found" when this used
	// 127.0.0.1, exactly like InstallZitadel's own authority variable above it (line ~658)
	// already uses "localhost" for the identical reason.
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/oauth/v2/token", zitadelPort),
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(zitadel.APIClientID, zitadel.APIClientSecret)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("token response had no access_token: %s", string(body))
	}
	return out.AccessToken, nil
}

// ensureSeedResource GETs listPath (bearer-authenticated) and, unless an entry named name
// already exists, POSTs {"name": name} to createPath.
func ensureSeedResource(base, token, listPath, createPath, name string) error {
	exists, err := seedNameExists(base, token, listPath, name)
	if err != nil {
		return fmt.Errorf("check existing: %w", err)
	}
	if exists {
		return nil
	}

	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return fmt.Errorf("marshal create request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, respBody, err := seedHTTPDoWithRetry(client, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodPost, base+createPath, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("create %q: %w", name, err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create %q: HTTP %d: %s", name, resp.StatusCode, string(respBody))
	}
	return nil
}

func seedNameExists(base, token, listPath, name string) (bool, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, body, err := seedHTTPDoWithRetry(client, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, base+listPath, nil)
		if err != nil {
			return nil, fmt.Errorf("build list request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
	if err != nil {
		return false, fmt.Errorf("list: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("list: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var items []seedResource
	if err := json.Unmarshal(body, &items); err != nil {
		return false, fmt.Errorf("decode list response: %w", err)
	}
	for _, item := range items {
		if item.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// seedHTTPDoWithRetry runs a request built fresh by newReq (so a POST body reader can be
// re-read on each attempt), retrying a bounded number of times on 502/503/504 responses.
// Traefik's Kubernetes Gateway provider updates its own routing table from Pod endpoints
// asynchronously, so the very first request against the platform right after InstallPlatform's
// `helm upgrade --wait` returns can race that sync and see a transient Bad
// Gateway/Unavailable/Gateway Timeout even though the backing Pod is already Ready — reproduced
// live as a consistent (not occasional) "HTTP 504: Gateway Timeout" on SeedPlatformData's very
// first request of a run immediately following a fresh platform rollout, always succeeding
// within a couple of seconds on retry.
func seedHTTPDoWithRetry(client *http.Client, newReq func() (*http.Request, error)) (*http.Response, []byte, error) {
	const attempts = 6
	const backoff = 2 * time.Second

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff)
		}
		req, err := newReq()
		if err != nil {
			return nil, nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request: %w", err)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}
		switch resp.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
			continue
		}
		return resp, body, nil
	}
	return nil, nil, fmt.Errorf("gave up after %d attempts: %w", attempts, lastErr)
}
