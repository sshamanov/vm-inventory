package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is a minimal Prometheus HTTP API client.
// Uses net/http — no external Prometheus API dependency.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// NewClient creates a new Prometheus client.
func NewClient(prometheusURL string) (*Client, error) {
	u, err := url.Parse(prometheusURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Prometheus URL: %w", err)
	}
	return &Client{
		baseURL: u,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// QueryInstant performs an instant query against the Prometheus API.
func (c *Client) QueryInstant(ctx context.Context, query string) (*QueryResponse, error) {
	u := c.baseURL.JoinPath("/api/v1/query")
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()
	return c.doQuery(ctx, u)
}

// QueryRange performs a range query against the Prometheus API.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*QueryResponse, error) {
	u := c.baseURL.JoinPath("/api/v1/query_range")
	q := u.Query()
	q.Set("query", query)
	q.Set("start", fmt.Sprintf("%d", start.Unix()))
	q.Set("end", fmt.Sprintf("%d", end.Unix()))
	q.Set("step", fmt.Sprintf("%ds", int(step.Seconds())))
	u.RawQuery = q.Encode()
	return c.doQuery(ctx, u)
}

// IsAvailable checks whether the Prometheus API is reachable.
func (c *Client) IsAvailable(ctx context.Context) bool {
	u := c.baseURL.JoinPath("/api/v1/status/buildinfo")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// QueryResponse is the Prometheus API response structure.
type QueryResponse struct {
	Status string    `json:"status"`
	Data   QueryData `json:"data"`
	Error  string    `json:"error,omitempty"`
}

// QueryData holds the result for instant or range queries.
type QueryData struct {
	ResultType string      `json:"resultType"`
	Result     []MetricResult `json:"result"`
}

// MetricResult is a single metric value (instant) or series (range).
type MetricResult struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`  // [timestamp, value] for instant
	Values [][]interface{}   `json:"values"` // [[timestamp, value], ...] for range
}

func (c *Client) doQuery(ctx context.Context, u *url.URL) (*QueryResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying Prometheus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Prometheus returned %d: %s", resp.StatusCode, string(body))
	}

	var qr QueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if qr.Status != "success" {
		return nil, fmt.Errorf("Prometheus query error: %s", qr.Error)
	}

	return &qr, nil
}
