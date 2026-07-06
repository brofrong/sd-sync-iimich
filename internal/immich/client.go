package immich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const uploadTimeFormat = "2006-01-02T15:04:05.000Z"

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
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
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}
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

	body, pw := io.Pipe()
	m := multipart.NewWriter(pw)

	errChan := make(chan error, 1)
	go func() {
		defer func() {
			_ = m.Close()
			_ = pw.Close()
		}()

		createdAt := mtime.UTC().Format(uploadTimeFormat)
		for _, pair := range [][2]string{
			{"fileCreatedAt", createdAt},
			{"fileModifiedAt", createdAt},
			{"filename", filename},
			{"duration", "0"},
		} {
			if err := m.WriteField(pair[0], pair[1]); err != nil {
				errChan <- err
				return
			}
		}

		fw, err := m.CreateFormFile("assetData", filename)
		if err != nil {
			errChan <- err
			return
		}
		if _, err := io.Copy(fw, f); err != nil {
			errChan <- err
			return
		}

		if sidecarPath != "" {
			sf, err := os.Open(sidecarPath)
			if err == nil {
				sfw, err := m.CreateFormFile("sidecarData", filepath.Base(sidecarPath))
				if err != nil {
					sf.Close()
					errChan <- err
					return
				}
				if _, err := io.Copy(sfw, sf); err != nil {
					sf.Close()
					errChan <- err
					return
				}
				sf.Close()
			}
		}

		errChan <- nil
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/assets", body)
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
	if err != nil && !errors.Is(err, io.ErrClosedPipe) {
		_ = body.Close()
		return nil, err
	}

	var result UploadResponse
	if resp != nil && resp.Body != nil {
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			gErr := <-errChan
			return nil, errors.Join(gErr, fmt.Errorf("upload: %s", formatAPIError(resp.Status, b)))
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

func (c *Client) Ping() error {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/server/ping", nil)
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
