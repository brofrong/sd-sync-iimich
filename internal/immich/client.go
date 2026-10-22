package immich

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	uploadTimeFormat = "2006-01-02T15:04:05.000Z"

	// DefaultStallTimeout aborts an upload when the server stops accepting body bytes.
	DefaultStallTimeout = 2 * time.Minute
)

// ErrUploadStalled means no body bytes were sent for StallTimeout.
var ErrUploadStalled = errors.New("upload stalled")

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// StallTimeout replaces a whole-request timeout, so large videos on slow
	// links are not cut off while they still make progress.
	StallTimeout time.Duration
}

// StatusError is a non-2xx response from Immich.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	return "upload: " + e.Message
}

// Detail returns a short, language-agnostic reason for err suitable for
// substitution into a localized UI message: the underlying OS/DNS failure
// text for network errors, the server's own message for a StatusError, or
// the error's own text as a fallback.
func Detail(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Err.Error()
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Message
	}
	return err.Error()
}

// IsRetryable reports whether an upload error is transient (network, stall,
// server overload) rather than a rejection of the file itself.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == http.StatusRequestTimeout || se.Code == http.StatusTooManyRequests || se.Code >= 500
	}
	if errors.Is(err, ErrUploadStalled) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var opErr *net.OpError
	var dnsErr *net.DNSError
	if errors.As(err, &opErr) || errors.As(err, &dnsErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

type UploadResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type APIError struct {
	Message string `json:"message"`
	Errors  []struct {
		Path    []string `json:"path"`
		Message string   `json:"message"`
	} `json:"errors"`
}

func formatAPIError(status string, body []byte) string {
	var apiErr APIError
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Message != "" {
		if len(apiErr.Errors) > 0 {
			return fmt.Sprintf("%s: %s (%s)", status, apiErr.Message, apiErr.Errors[0].Message)
		}
		return fmt.Sprintf("%s: %s", status, apiErr.Message)
	}
	return fmt.Sprintf("%s: %s", status, string(body))
}

func NewClient(baseURL, apiKey string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/api") {
		baseURL += "/api"
	}
	return &Client{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		HTTP:         &http.Client{Transport: newTransport()},
		StallTimeout: DefaultStallTimeout,
	}
}

// newTransport forces HTTP/1.1: each worker gets its own TCP connection.
// Over a high-latency link one multiplexed HTTP/2 connection uploaded 2-3x
// slower than four HTTP/1.1 connections.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig:       &tls.Config{NextProtos: []string{"http/1.1"}},
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		WriteBufferSize:       256 << 10,
	}
}

// progressReader calls onRead after every read and onEOF once the body is fully consumed.
// Close must reach the pipe so the multipart writer stops when the server answers early.
type progressReader struct {
	r      io.ReadCloser
	onRead func()
	onEOF  func()
}

func (p *progressReader) Close() error {
	return p.r.Close()
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.onRead()
	}
	if err == io.EOF {
		p.onEOF()
	}
	return n, err
}

func (c *Client) Upload(ctx context.Context, filePath, sha1hex string, mtime time.Time, sidecarPath string) (*UploadResponse, error) {
	filename := filepath.Base(filePath)
	if filename == "" {
		return nil, fmt.Errorf("upload: empty filename for %q", filePath)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stall := c.StallTimeout
	if stall <= 0 {
		stall = DefaultStallTimeout
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(stall, func() { cancel(ErrUploadStalled) })
	defer watchdog.Stop()

	body, pw := io.Pipe()
	m := multipart.NewWriter(pw)

	errChan := make(chan error, 1)
	go func() {
		var werr error
		defer func() {
			if werr != nil {
				_ = pw.CloseWithError(werr)
				errChan <- werr
				return
			}
			_ = m.Close()
			_ = pw.Close()
			errChan <- nil
		}()

		createdAt := mtime.UTC().Format(uploadTimeFormat)
		for _, pair := range [][2]string{
			{"fileCreatedAt", createdAt},
			{"fileModifiedAt", createdAt},
			{"filename", filename},
			{"duration", "0"},
		} {
			if err := m.WriteField(pair[0], pair[1]); err != nil {
				werr = err
				return
			}
		}

		fw, err := m.CreateFormFile("assetData", filename)
		if err != nil {
			werr = err
			return
		}
		if _, err := io.Copy(fw, f); err != nil {
			werr = err
			return
		}

		if sidecarPath != "" {
			sf, err := os.Open(sidecarPath)
			if err == nil {
				sfw, err := m.CreateFormFile("sidecarData", filepath.Base(sidecarPath))
				if err != nil {
					sf.Close()
					werr = err
					return
				}
				if _, err := io.Copy(sfw, sf); err != nil {
					sf.Close()
					werr = err
					return
				}
				sf.Close()
			}
		}
	}()

	reqBody := &progressReader{
		r:      body,
		onRead: func() { watchdog.Reset(stall) },
		// After the body is sent, ResponseHeaderTimeout covers server processing.
		onEOF: func() { watchdog.Stop() },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/assets", reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", m.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	if sha1hex != "" {
		req.Header.Set("x-immich-checksum", sha1hex)
	}

	resp, err := c.HTTP.Do(req)
	watchdog.Stop()
	if err != nil && !errors.Is(err, io.ErrClosedPipe) {
		_ = body.Close()
		<-errChan
		if errors.Is(context.Cause(ctx), ErrUploadStalled) {
			return nil, fmt.Errorf("%w after %s without progress", ErrUploadStalled, stall)
		}
		return nil, err
	}

	var result UploadResponse
	if resp != nil && resp.Body != nil {
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			gErr := <-errChan
			return nil, errors.Join(gErr, &StatusError{Code: resp.StatusCode, Message: formatAPIError(resp.Status, b)})
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			resp.Body.Close()
			gErr := <-errChan
			return nil, errors.Join(gErr, err)
		}
		resp.Body.Close()
	}

	gErr := <-errChan
	err = errors.Join(err, gErr)
	if result.Status == "duplicate" && errors.Is(err, io.ErrClosedPipe) {
		err = nil
	}
	if result.Status == "" && sha1hex != "" && errors.Is(err, io.ErrClosedPipe) {
		result.Status = "duplicate"
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// Ping checks reachability of the server, honoring ctx so callers (such as
// auto-resume) can bound or cancel the check.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/server/ping", nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping: %s", resp.Status)
	}
	return nil
}

// ServerInfo describes the Immich server a Client is pointed at.
type ServerInfo struct {
	Version string
}

type serverVersionResponse struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

// Check verifies the server is reachable and that the API key has upload
// permission, for the "Test connection" button. It first fetches the server
// version (no auth required), then confirms the key can call the
// assets/bulk-upload-check endpoint; a non-2xx response there (e.g. a bad or
// read-only key) comes back as a *StatusError.
func (c *Client) Check(ctx context.Context) (ServerInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/server/version", nil)
	if err != nil {
		return ServerInfo{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ServerInfo{}, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return ServerInfo{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return ServerInfo{}, &StatusError{Code: resp.StatusCode, Message: formatAPIError(resp.Status, body)}
	}
	var v serverVersionResponse
	if err := json.Unmarshal(body, &v); err != nil {
		return ServerInfo{}, err
	}
	info := ServerInfo{Version: fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)}

	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/assets/bulk-upload-check", strings.NewReader(`{"assets":[]}`))
	if err != nil {
		return ServerInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	resp, err = c.HTTP.Do(req)
	if err != nil {
		return ServerInfo{}, err
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return ServerInfo{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return ServerInfo{}, &StatusError{Code: resp.StatusCode, Message: formatAPIError(resp.Status, body)}
	}
	return info, nil
}
