package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hsync/internal/protocol"
	"hsync/internal/utils"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

func newRequest(cfg *Config, method, path, query string, body io.Reader) (*http.Request, error) {
	target := cfg.ServerURL + path
	if query != "" {
		target += "?" + query
	}

	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Sync-Key", cfg.Key)
	return req, nil
}

func fetchFileList(cfg *Config, client *http.Client) (map[string]string, error) {
	req, err := newRequest(cfg, http.MethodGet, "/sync", "", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	files := make(map[string]string)
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	return files, nil
}

func fetchTombstones(cfg *Config, client *http.Client) (map[string]protocol.Tombstone, error) {
	req, err := newRequest(cfg, http.MethodGet, "/deleted", "", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return map[string]protocol.Tombstone{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	tombstones := make(map[string]protocol.Tombstone)
	if err := json.NewDecoder(resp.Body).Decode(&tombstones); err != nil {
		return nil, err
	}
	return tombstones, nil
}

const maxDownloadBackoff = 8 * time.Second

var errNotFound = fmt.Errorf("not found on the server")

func downloadFile(cfg *Config, client *http.Client, filename string) (string, error) {
	var lastErr error
	maxRetries := 10
	backoff := 500 * time.Millisecond

	for i := 0; i <= maxRetries; i++ {
		if i > 0 {
			time.Sleep(backoff)
			if backoff < maxDownloadBackoff {
				backoff *= 2
			}
		}

		query := url.Values{"filename": {filename}}.Encode()
		req, err := newRequest(cfg, http.MethodGet, "/sync", query, nil)
		if err != nil {
			return "", err
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			log.Printf("Attempt %d failed to download %s: %v", i+1, filename, err)
			continue
		}

		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return "", errNotFound
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			log.Printf("Attempt %d failed to download %s: status %d", i+1, filename, resp.StatusCode)
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			log.Printf("Attempt %d failed to read body for %s: %v", i+1, filename, err)
			continue
		}
		return string(data), nil
	}

	return "", fmt.Errorf("failed to download %s after %d attempts: %v", filename, maxRetries+1, lastErr)
}

var errUploadConflict = fmt.Errorf("file was moved or deleted on the server")

func uploadFile(cfg *Config, client *http.Client, filename, base, current string) (string, error) {
	jsonBody, err := json.Marshal(protocol.SyncRequest{
		Filename: filename,
		Base:     base,
		Latest:   current,
	})
	if err != nil {
		return "", err
	}

	req, err := newRequest(cfg, http.MethodPost, "/sync", "", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return "", errUploadConflict
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var syncResp protocol.SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return "", err
	}
	return syncResp.Synced, nil
}

var errDeleteConflict = fmt.Errorf("server copy changed")

func deleteRemoteFile(cfg *Config, client *http.Client, filename, base string) error {
	query := url.Values{
		"filename": {filename},
		"base":     {utils.CalculateHash(base)},
	}.Encode()

	req, err := newRequest(cfg, http.MethodDelete, "/sync", query, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusConflict:
		return errDeleteConflict
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
}

func renameRemoteFile(cfg *Config, client *http.Client, from, to, base string) (string, error) {
	jsonBody, err := json.Marshal(protocol.RenameRequest{From: from, To: to, Base: base})
	if err != nil {
		return "", err
	}

	req, err := newRequest(cfg, http.MethodPost, "/rename", "", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var syncResp protocol.SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return "", err
	}
	return syncResp.Synced, nil
}
