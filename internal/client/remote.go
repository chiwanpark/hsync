package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hsync/internal/protocol"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var (
	errUnknownParent = errors.New("the server does not know the parent commit")
	errMissingBlob   = errors.New("the server is missing an uploaded note")
)

func normalizeServerURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")

	parsed, err := url.Parse(trimmed)
	switch {
	case err != nil:
		return "", err
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return "", fmt.Errorf("expected an http or https URL, got %q", raw)
	case parsed.Host == "":
		return "", fmt.Errorf("missing host in %q", raw)
	case parsed.RawQuery != "" || parsed.Fragment != "":
		return "", fmt.Errorf("expected a plain URL without a query or fragment, got %q", raw)
	}
	return trimmed, nil
}

func fetchIndex(cfg *Config, client *http.Client) (protocol.IndexResponse, error) {
	var index protocol.IndexResponse

	resp, err := send(cfg, client, http.MethodGet, "/index", nil)
	if err != nil {
		return index, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return index, describe(resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return index, err
	}
	if index.Files == nil {
		index.Files = map[string]string{}
	}
	return index, nil
}

func fetchBlob(cfg *Config, client *http.Client, hash string) (string, error) {
	resp, err := send(cfg, client, http.MethodGet, "/blob?"+url.Values{"hash": {hash}}.Encode(), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", describe(resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	return string(content), err
}

func pushCommit(cfg *Config, client *http.Client, parent string, files, blobs map[string]string) (protocol.PushResponse, error) {
	var result protocol.PushResponse

	body, err := json.Marshal(protocol.PushRequest{Parent: parent, Files: files, Blobs: blobs})
	if err != nil {
		return result, err
	}

	resp, err := send(cfg, client, http.MethodPost, "/push", bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusConflict:
		return result, errUnknownParent
	case http.StatusUnprocessableEntity:
		return result, errMissingBlob
	default:
		return result, describe(resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, err
	}
	if result.Files == nil {
		result.Files = map[string]string{}
	}
	return result, nil
}

func send(cfg *Config, client *http.Client, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, cfg.ServerURL+path, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Sync-Key", cfg.Key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return client.Do(req)
}

func describe(status int) error {
	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("status 404, check the server URL and that the reverse proxy strips its path prefix")
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("status 413, raise the request body limit of the reverse proxy")
	case http.StatusMethodNotAllowed:
		return fmt.Errorf("status 405, the reverse proxy may have turned the request into a GET while redirecting")
	default:
		return fmt.Errorf("status %d", status)
	}
}
