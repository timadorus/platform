package cliapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Client issues HTTP requests against the command-api and query-api base URLs from Config,
// attaching the bearer token to every request. Every command in this package goes through
// Command/Query so response handling (JSON-to-stdout, problem+json-to-stderr, exit status)
// stays consistent — see docs/PLAN.md §14.
type Client struct {
	cfg  Config
	http *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

// Command issues method/path against the command-api with an optional JSON body (nil for
// none), printing the response to stdout/stderr per handleResponse.
func (c *Client) Command(method, path string, body any) error {
	return c.do(method, c.cfg.CommandAPIURL, path, body)
}

// Query issues a GET against the query-api, printing the response to stdout/stderr per
// handleResponse.
func (c *Client) Query(path string) error {
	return c.do(http.MethodGet, c.cfg.QueryAPIURL, path, nil)
}

func (c *Client) do(method, baseURL, path string, body any) error {
	if c.cfg.Token == "" {
		return fmt.Errorf("no token configured — run `timadorusctl auth set-token <jwt>` first (see docs/PLAN.md §14)")
	}

	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cliapp: encode request body: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("cliapp: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cliapp: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("cliapp: read response body: %w", err)
	}

	return handleResponse(resp.StatusCode, respBody)
}

// handleResponse prints exactly one thing to stdout on success — the response body,
// pretty-printed if it's JSON — so stdout is always either an aggregate/projection as JSON
// or nothing, per the CLI's contract. Errors (and all other human-facing text: prompts,
// "saved as current X" confirmations) go to stderr, never stdout.
func handleResponse(status int, body []byte) error {
	if status >= 400 {
		fmt.Fprintln(os.Stderr, prettyOrRaw(body))
		return fmt.Errorf("request failed: HTTP %d", status)
	}
	if len(body) == 0 {
		fmt.Println(`{"status":"ok"}`)
		return nil
	}
	fmt.Println(prettyOrRaw(body))
	return nil
}

func prettyOrRaw(body []byte) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		return string(body)
	}
	return pretty.String()
}
