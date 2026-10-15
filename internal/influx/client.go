package influx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to InfluxDB. Reads always go to URL; writes go to WriteURL,
// which is URL unless writes are routed through Telegraf.
type Client struct {
	URL        string
	Token      string
	WriteURL   string
	WriteToken string
	Org        string
	Bucket     string
	HTTP       *http.Client
}

// batchSize is the number of lines per write request.
const batchSize = 5000

// Write sends points as line protocol. An empty slice is a no-op.
func (c *Client) Write(ctx context.Context, points []Point) error {
	for len(points) > 0 {
		n := min(len(points), batchSize)
		if err := c.write(ctx, points[:n]); err != nil {
			return err
		}
		points = points[n:]
	}
	return nil
}

func (c *Client) write(ctx context.Context, points []Point) error {
	var body bytes.Buffer
	for _, p := range points {
		line, err := p.Line()
		if err != nil {
			return err
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	q := url.Values{"org": {c.Org}, "bucket": {c.Bucket}, "precision": {"s"}}
	base, token := c.WriteURL, c.WriteToken
	if base == "" {
		base, token = c.URL, c.Token
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/v2/write?"+q.Encode(), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	setToken(req, token)
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("influx write: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("influx write: HTTP %d: %s", resp.StatusCode, excerpt(resp.Body))
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// Series is one series of an InfluxQL result.
type Series struct {
	Name    string            `json:"name"`
	Tags    map[string]string `json:"tags"`
	Columns []string          `json:"columns"`
	Values  [][]any           `json:"values"`
}

type queryResponse struct {
	Results []struct {
		Series []Series `json:"series"`
		Error  string   `json:"error"`
	} `json:"results"`
	Error string `json:"error"`
}

// Query runs one InfluxQL statement against the bucket (InfluxDB maps the
// bucket name to a database) and returns its series. Times are Unix seconds.
func (c *Client) Query(ctx context.Context, influxQL string) ([]Series, error) {
	form := url.Values{"db": {c.Bucket}, "q": {influxQL}, "epoch": {"s"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/query", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	setToken(req, c.Token)
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("influx query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("influx query: HTTP %d: %s", resp.StatusCode, excerpt(resp.Body))
	}
	var qr queryResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, 64<<20))
	dec.UseNumber()
	if err := dec.Decode(&qr); err != nil {
		return nil, fmt.Errorf("influx query: %w", err)
	}
	if qr.Error != "" {
		return nil, fmt.Errorf("influx query: %s", qr.Error)
	}
	var out []Series
	for _, r := range qr.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("influx query: %s", r.Error)
		}
		out = append(out, r.Series...)
	}
	return out, nil
}

// Ping checks that InfluxDB answers (GET /ping, no token needed).
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+"/ping", nil)
	if err != nil {
		return err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("influx ping: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("influx ping: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func setToken(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Token "+token)
	}
}

func excerpt(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 300))
	return strings.TrimSpace(string(b))
}

// QuoteString quotes a string literal for InfluxQL.
func QuoteString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// QuoteIdent quotes an identifier for InfluxQL.
func QuoteIdent(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Time literal for InfluxQL (RFC 3339, UTC).
func Time(t time.Time) string {
	return "'" + t.UTC().Format(time.RFC3339) + "'"
}

// ErrNoData is returned by lookups that found nothing.
var ErrNoData = errors.New("influx: no data")
