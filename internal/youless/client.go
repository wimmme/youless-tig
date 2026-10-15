package youless

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is the device's 404: a log page beyond what it keeps.
var ErrNotFound = errors.New("youless: not found")

// ErrUnauthorized means the device wants a password (403) and the configured
// one is missing or wrong.
var ErrUnauthorized = errors.New("youless: password required or wrong")

// maxBody limits what is read from the device; its largest answer is a few kB.
const maxBody = 1 << 20

// Client talks to one YouLess. Requests are serialised and spaced by Gap: the
// device is small and answers one request at a time.
type Client struct {
	base     *url.URL
	password string
	http     *http.Client
	Gap      time.Duration

	mu     sync.Mutex
	cookie string
	last   time.Time
}

// NewClient returns a client for the device at base (http://host[:port]).
// password may be empty.
func NewClient(base string, password string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("youless URL %q: want http://host[:port]", base)
	}
	return &Client{
		base:     u,
		password: password,
		Gap:      200 * time.Millisecond,
		http: &http.Client{
			Timeout: timeout,
			// The login answers with a redirect we must see, not follow.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Get fetches a path such as "/e" or "/V?h=1" and returns the body. On a 403
// it logs in with the password (if any) and tries once more.
func (c *Client) Get(ctx context.Context, pathQuery string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, status, err := c.do(ctx, pathQuery, true)
	if err == nil && status == http.StatusForbidden && c.password != "" {
		if err = c.login(ctx); err != nil {
			return nil, err
		}
		body, status, err = c.do(ctx, pathQuery, true)
	}
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusForbidden:
		return nil, ErrUnauthorized
	}
	return nil, fmt.Errorf("youless %s: HTTP %d", pathQuery, status)
}

// do performs one request, waiting for the gap since the previous one.
// Callers hold c.mu.
func (c *Client) do(ctx context.Context, pathQuery string, withCookie bool) ([]byte, int, error) {
	if wait := c.Gap - time.Since(c.last); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, 0, ctx.Err()
		case <-t.C:
		}
	}
	defer func() { c.last = time.Now() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+pathQuery, nil)
	if err != nil {
		return nil, 0, err
	}
	if withCookie && c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL of a login request holds the password: never pass it on.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, 0, fmt.Errorf("youless %s: %w", redact(pathQuery), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, 0, fmt.Errorf("youless %s: %w", redact(pathQuery), err)
	}
	if sc := resp.Header.Values("Set-Cookie"); len(sc) > 0 {
		var parts []string
		for _, s := range sc {
			parts = append(parts, strings.TrimSpace(strings.SplitN(s, ";", 2)[0]))
		}
		c.cookie = strings.Join(parts, "; ")
	}
	return body, resp.StatusCode, nil
}

// login sends the password the way the device's web interface does
// (GET /L?w=<password>); a correct password answers with a redirect and a cookie.
func (c *Client) login(ctx context.Context) error {
	c.cookie = ""
	_, status, err := c.do(ctx, "/L?w="+url.QueryEscape(c.password), false)
	if err != nil {
		return err
	}
	if status != http.StatusFound && status != http.StatusOK {
		return ErrUnauthorized
	}
	return nil
}

// redact hides the password of a login path in messages.
func redact(pathQuery string) string {
	if strings.HasPrefix(pathQuery, "/L?") {
		return "/L?w=***"
	}
	return pathQuery
}

// Device reads /d.
func (c *Client) Device(ctx context.Context) (*Device, []byte, error) {
	b, err := c.Get(ctx, "/d")
	if err != nil {
		return nil, nil, err
	}
	d, err := ParseDevice(b)
	return d, b, err
}

// Energy reads /e.
func (c *Client) Energy(ctx context.Context) (*Energy, []byte, error) {
	b, err := c.Get(ctx, "/e")
	if err != nil {
		return nil, nil, err
	}
	e, err := ParseEnergy(b)
	return e, b, err
}

// Phases reads /f.
func (c *Client) Phases(ctx context.Context) (*Phases, []byte, error) {
	b, err := c.Get(ctx, "/f")
	if err != nil {
		return nil, nil, err
	}
	f, err := ParsePhases(b)
	return f, b, err
}

// Log reads one log page. For the day log, page is the month number.
func (c *Client) Log(ctx context.Context, ch Channel, r Resolution, page int, loc *time.Location) (*LogPage, error) {
	b, err := c.Get(ctx, ch.Path()+"?"+r.Param()+"="+strconv.Itoa(page)+"&f=j")
	if err != nil {
		return nil, err
	}
	return ParseLogPage(b, loc)
}
