package mirakurun

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	UserAgent  string
	Priority   int
}

func New(raw string) (*Client, error) {
	if raw == "" {
		raw = "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/"
	}
	c := &Client{UserAgent: "Chinachu-Go"}
	if strings.HasPrefix(raw, "http+unix://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		socketPath, err := url.PathUnescape(u.Host)
		if err != nil {
			return nil, err
		}
		basePath := u.EscapedPath()
		c.baseURL = &url.URL{Scheme: "http", Host: "unix", Path: basePath}
		c.httpClient = unixHTTPClient(socketPath)
		return c, nil
	}
	if strings.HasPrefix(raw, "http://unix:") {
		rest := strings.TrimPrefix(raw, "http://unix:")
		parts := strings.SplitN(rest, ":", 2)
		socketPath := parts[0]
		basePath := "/"
		if len(parts) == 2 {
			basePath = parts[1]
		}
		c.baseURL = &url.URL{Scheme: "http", Host: "unix", Path: basePath}
		c.httpClient = unixHTTPClient(socketPath)
		return c, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	c.baseURL = u
	c.httpClient = &http.Client{Timeout: 30 * time.Second}
	return c, nil
}

func unixHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 30 * time.Second,
	}
}

func (c *Client) GetJSON(ctx context.Context, endpoint string, dst any) error {
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("mirakurun %s: %s", endpoint, res.Status)
	}
	return decodeJSON(res.Body, dst)
}

func (c *Client) Services(ctx context.Context) ([]Service, error) {
	var services []Service
	err := c.GetJSON(ctx, "/api/services", &services)
	return services, err
}

func (c *Client) Programs(ctx context.Context) ([]Program, error) {
	var programs []Program
	err := c.GetJSON(ctx, "/api/programs", &programs)
	return programs, err
}

func (c *Client) Tuners(ctx context.Context) ([]Tuner, error) {
	var tuners []Tuner
	err := c.GetJSON(ctx, "/api/tuners", &tuners)
	return tuners, err
}

func (c *Client) ProgramStream(ctx context.Context, id int64, decode bool) (io.ReadCloser, error) {
	endpoint := fmt.Sprintf("/api/programs/%d/stream", id)
	if decode {
		endpoint += "?decode=1"
	}
	return c.stream(ctx, endpoint)
}

func (c *Client) ServiceStream(ctx context.Context, id int64, decode bool) (io.ReadCloser, error) {
	endpoint := fmt.Sprintf("/api/services/%d/stream", id)
	if decode {
		endpoint += "?decode=1"
	}
	return c.stream(ctx, endpoint)
}

func (c *Client) LogoImage(ctx context.Context, id int64) (io.ReadCloser, error) {
	return c.stream(ctx, fmt.Sprintf("/api/services/%d/logo", id))
}

func (c *Client) stream(ctx context.Context, endpoint string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		return nil, fmt.Errorf("mirakurun stream %s: %s", endpoint, res.Status)
	}
	return res.Body, nil
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	u := *c.baseURL
	u.Path = path.Join(c.baseURL.Path, endpoint)
	if strings.Contains(endpoint, "?") {
		parts := strings.SplitN(endpoint, "?", 2)
		u.Path = path.Join(c.baseURL.Path, parts[0])
		u.RawQuery = parts[1]
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if c.Priority != 0 {
		req.Header.Set("X-Mirakurun-Priority", fmt.Sprintf("%d", c.Priority))
	}
	return req, nil
}
