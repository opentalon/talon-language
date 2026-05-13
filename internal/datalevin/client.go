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
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient creates a client pointing at the given base URL (e.g. "http://localhost:8898").
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: http.DefaultClient,
	}
}

// QueryResult is the parsed response from POST /q.
type QueryResult struct {
	Results [][]any `json:"results"`
}

// Query executes a Datalog query and returns result tuples.
func (c *Client) Query(ctx context.Context, query string) ([][]any, error) {
	body := map[string]any{"query": query}
	var result QueryResult
	if err := c.post(ctx, "/q", body, &result); err != nil {
		return nil, fmt.Errorf("datalevin query: %w", err)
	}
	return result.Results, nil
}

// Transact asserts facts into the database.
// Each fact is a map of attribute string → value (e.g. {":record/type": "item"}).
func (c *Client) Transact(ctx context.Context, txData []map[string]any) error {
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
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, bytes.NewReader(jsonBody))
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
