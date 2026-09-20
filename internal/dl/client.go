package dl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const userAgent = "airgapkit/1.0 (+offline mirror builder)"

// Client is a small retrying HTTP client shared by every ecosystem module.
type Client struct {
	HTTP    *http.Client
	Retries int
	// Verbose prints one line per request when true.
	Verbose bool
}

// New returns a Client with sane timeouts for large artifact downloads.
func New() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 30 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 16,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 30 * time.Second,
				ForceAttemptHTTP2:   true,
			},
		},
		Retries: 4,
	}
}

// StatusError reports a non-2xx HTTP response.
type StatusError struct {
	Code int
	URL  string
	Body string
}

func (e *StatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("GET %s: %d %s: %s", e.URL, e.Code, http.StatusText(e.Code), e.Body)
	}
	return fmt.Sprintf("GET %s: %d %s", e.URL, e.Code, http.StatusText(e.Code))
}

// IsNotFound reports whether err is a 404/410 StatusError.
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code == http.StatusGone)
}

func retryable(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500
}

// Do performs a request with retries and hands the response to fn. The
// response body is closed by Do. fn may be called more than once.
func (c *Client) Do(ctx context.Context, method, url string, header http.Header, fn func(*http.Response) error) error {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		if c.Verbose {
			fmt.Fprintf(os.Stderr, "  %s %s\n", method, url)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode/100 != 2 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			se := &StatusError{Code: resp.StatusCode, URL: url, Body: string(body)}
			if !retryable(resp.StatusCode) {
				return se
			}
			lastErr = se
			continue
		}
		err = fn(resp)
		resp.Body.Close()
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// GetBytes downloads a small resource (index, manifest) fully into memory.
func (c *Client) GetBytes(ctx context.Context, url string, header http.Header) ([]byte, http.Header, error) {
	var out []byte
	var hdr http.Header
	err := c.Do(ctx, http.MethodGet, url, header, func(resp *http.Response) error {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		out, hdr = b, resp.Header.Clone()
		return nil
	})
	return out, hdr, err
}

// Download streams url into dest, verifying want when it is set. It always
// returns the sha256 of the bytes written. The file is written to a temporary
// name in the same directory and renamed on success, so a partial download
// never lands in the mirror.
func (c *Client) Download(ctx context.Context, url, dest string, want Expect, header http.Header) (size int64, sha256hex string, err error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, "", err
	}
	err = c.Do(ctx, http.MethodGet, url, header, func(resp *http.Response) error {
		tmp, err := os.CreateTemp(filepath.Dir(dest), ".airgap-*.part")
		if err != nil {
			return err
		}
		defer func() {
			tmp.Close()
			os.Remove(tmp.Name())
		}()

		sum := sha256.New()
		var check = sum
		var verify func() error
		if want.Algo != "" && want.Algo != "sha256" {
			h := newHasher(want.Algo)
			n, werr := io.Copy(io.MultiWriter(tmp, sum, h), resp.Body)
			if werr != nil {
				return werr
			}
			size = n
			verify = func() error {
				if got := hex.EncodeToString(h.Sum(nil)); got != want.Value {
					return fmt.Errorf("%s mismatch for %s: want %s got %s", want.Algo, url, want.Value, got)
				}
				return nil
			}
		} else {
			n, werr := io.Copy(io.MultiWriter(tmp, check), resp.Body)
			if werr != nil {
				return werr
			}
			size = n
		}
		sha256hex = hex.EncodeToString(sum.Sum(nil))
		if want.Algo == "sha256" && sha256hex != want.Value {
			return fmt.Errorf("sha256 mismatch for %s: want %s got %s", url, want.Value, sha256hex)
		}
		if verify != nil {
			if err := verify(); err != nil {
				return err
			}
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), dest)
	})
	if err != nil {
		return 0, "", err
	}
	return size, sha256hex, nil
}

// Request performs a single request and hands the live response to the
// caller, which must close the body. Unlike Do it does not treat a non-2xx
// status as an error, so callers can inspect authentication challenges.
func (c *Client) Request(ctx context.Context, method, url string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return c.HTTP.Do(req)
}
