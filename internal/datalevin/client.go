package datalevin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client talks to the datalevin-server HTTP service.
//
// Tenant is the per-request URL prefix: when set, every request is
// posted to `<BaseURL>/t/<Tenant>/...` so the server can route to a
// separate Datalevin DB per tenant. Empty Tenant uses the
// server's default DB (the original single-tenant layout).
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Tenant     string
}

// NewClient creates a client pointing at the given base URL (e.g. "http://localhost:8898").
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: http.DefaultClient,
	}
}

// WithTenant returns a clone of c that targets the named tenant. The
// underlying transport is shared so callers can hand the same Client
// to one tenant and another concurrently.
func (c *Client) WithTenant(name string) *Client {
	cp := *c
	cp.Tenant = name
	return &cp
}

// tenantPath returns the URL path prefix for this client's tenant.
// Empty for the default tenant; "/t/<name>" otherwise. Combined with
// the request path (e.g. "/q") it produces the final URL.
func (c *Client) tenantPath() string {
	if c.Tenant == "" {
		return ""
	}
	return "/t/" + c.Tenant
}

// QueryResult is the parsed response from POST /q.
type QueryResult struct {
	Results [][]any `json:"results"`
}

// RawQuery executes a raw Datalog query string. The structured Query method
// is the one the executor calls; RawQuery is exposed for the few call sites
// (notably `tln trace --raw` and the CLI seeding hot-path) that already
// have hand-written Datalog text.
func (c *Client) RawQuery(ctx context.Context, query string) ([][]any, error) {
	body := map[string]any{"query": query}
	var result QueryResult
	if err := c.post(ctx, "/q", body, &result); err != nil {
		return nil, fmt.Errorf("datalevin query: %w", err)
	}
	return result.Results, nil
}

// RawQueryWithRules is the recursive-rules variant of RawQuery. The
// `rules` string is Datalevin's rule-vector form (see
// factstore.Query.RulesString); the query must declare `% ` in its
// :in clause so the server can pass them through to d/q.
func (c *Client) RawQueryWithRules(ctx context.Context, query, rules string) ([][]any, error) {
	return c.RawQueryFull(ctx, query, rules, nil)
}

// RawQueryFull is the most general wire-format variant. `rules` is
// Datalevin's rule-vector form (empty when none); `args` is the list
// of extra positional `:in` parameters (each a Clojure-readable
// string the server `read-string`s back into a value). Order:
// the query declares `:in $ [%] [?fts-q-0 ...]` — `%` (if rules) is
// passed first, then args in declaration order.
//
// Today only factstore.FullText.Expr injects args (Datalevin's
// structured search expressions don't survive inside a :where
// clause). The shape generalises so future clauses can ride the
// same channel without growing the public surface.
func (c *Client) RawQueryFull(ctx context.Context, query, rules string, args []string) ([][]any, error) {
	body := map[string]any{"query": query}
	if rules != "" {
		body["rules"] = rules
	}
	if len(args) > 0 {
		body["args"] = args
	}
	var result QueryResult
	if err := c.post(ctx, "/q", body, &result); err != nil {
		return nil, fmt.Errorf("datalevin query: %w", err)
	}
	return result.Results, nil
}

// RawTransact submits raw Datalevin transaction maps. Use Assert (which
// satisfies the factstore.FactStore interface) for the normal path.
func (c *Client) RawTransact(ctx context.Context, txData []map[string]any) error {
	body := map[string]any{"tx-data": txData}
	var result map[string]any
	if err := c.post(ctx, "/transact", body, &result); err != nil {
		return fmt.Errorf("datalevin transact: %w", err)
	}
	return nil
}

// Schema registers database schema attributes.
// attrs maps attribute name → property map (e.g. {":attr/km": {"db/valueType": "db.type/long"}}).
func (c *Client) Schema(ctx context.Context, attrs map[string]map[string]string) error {
	body := map[string]any{"attrs": attrs}
	var result map[string]any
	if err := c.post(ctx, "/schema", body, &result); err != nil {
		return fmt.Errorf("datalevin schema: %w", err)
	}
	return nil
}

// SchemaRich is Schema for attribute specs where some values are
// lists (e.g. :db.fulltext/domains taking ["docs" "items"]). The
// server's coerce-schema-value recursively maps vector entries to
// keywords, so callers can mix string and list values per attr.
func (c *Client) SchemaRich(ctx context.Context, attrs map[string]map[string]any) error {
	body := map[string]any{"attrs": attrs}
	var result map[string]any
	if err := c.post(ctx, "/schema", body, &result); err != nil {
		return fmt.Errorf("datalevin schema: %w", err)
	}
	return nil
}

// Health checks if the server is alive.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("datalevin health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("datalevin health: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, body any, result any) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+c.tenantPath()+path, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return json.Unmarshal(respBody, result)
}
