// Package jsonplaceholder is a typed client for the free JSONPlaceholder API.
//
// It demonstrates the standard-library pattern for calling a third-party HTTP
// API:
//
//   - construct requests with a context so callers can cancel or time them out;
//   - set explicit headers (Accept, Content-Type, User-Agent);
//   - decode responses into typed Go structs;
//   - turn non-2xx responses into a typed error that callers can inspect;
//   - limit how much of a response body is read;
//   - leave retries, caching, and authentication to the caller or a wrapper.
//
// JSONPlaceholder is a fake API. POST/PUT/DELETE requests return realistic
// responses, but the server does not actually persist changes.
package jsonplaceholder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultBaseURL is the public JSONPlaceholder endpoint.
	DefaultBaseURL = "https://jsonplaceholder.typicode.com"

	// defaultTimeout is used when the caller does not supply an http.Client.
	// In production, configure the timeout at the composition root instead.
	defaultTimeout = 10 * time.Second

	// maxResponseBytes prevents a misbehaving server from exhausting memory.
	maxResponseBytes = 1 << 20 // 1 MiB

	// maxErrorBodyRunes keeps HTTPError messages readable in logs.
	maxErrorBodyRunes = 300

	// userAgent identifies this learning client to the remote service.
	userAgent = "taskflow-jsonplaceholder-demo/1.0"
)

// Post is the resource shape used by the JSONPlaceholder /posts endpoints.
type Post struct {
	UserID int    `json:"userId"`
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// PostInput is the writable part of a post.
//
// ID is omitted because JSONPlaceholder assigns it. Keeping input and output
// types separate also prevents accidentally sending server-owned fields.
type PostInput struct {
	UserID int    `json:"userId"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// Client talks to a JSONPlaceholder-compatible API.
//
// The zero value is not usable; construct one with New.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// HTTPError represents a non-2xx response from the remote API.
//
// It is a concrete type so callers can use errors.As to inspect the status
// code and response body, for example to distinguish 404 from 500.
type HTTPError struct {
	Method     string // HTTP method used for the request.
	Path       string // Request path, without the base URL.
	StatusCode int    // HTTP status code returned by the server.
	Status     string // Full status text, e.g. "404 Not Found".
	Body       string // Truncated response body, useful for diagnostics.
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	message := fmt.Sprintf("%s %s: unexpected status %s", e.Method, e.Path, e.Status)
	if e.Body != "" {
		message += ": " + e.Body
	}
	return message
}

// New creates a client for baseURL.
//
// baseURL must be an absolute http or https URL. A trailing slash is accepted.
// If httpClient is nil, a client with a 10-second timeout is used.
func New(baseURL string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base URL must not be empty")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("base URL scheme must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("base URL must include a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("base URL must not include a query string or fragment")
	}

	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}, nil
}

// GetPost fetches one post with GET /posts/{id}.
func (c *Client) GetPost(ctx context.Context, id int) (Post, error) {
	if id <= 0 {
		return Post{}, fmt.Errorf("post id must be greater than zero")
	}

	var post Post
	path := fmt.Sprintf("/posts/%d", id)
	if err := c.do(ctx, http.MethodGet, path, nil, &post); err != nil {
		return Post{}, fmt.Errorf("get post %d: %w", id, err)
	}
	return post, nil
}

// CreatePost creates a post with POST /posts.
//
// JSONPlaceholder replies with 201 Created and invents an ID (commonly 101).
func (c *Client) CreatePost(ctx context.Context, input PostInput) (Post, error) {
	var created Post
	if err := c.do(ctx, http.MethodPost, "/posts", input, &created); err != nil {
		return Post{}, fmt.Errorf("create post: %w", err)
	}
	return created, nil
}

// UpdatePost replaces a post with PUT /posts/{id}.
//
// PUT is used because it represents a full replacement. A partial update would
// normally use PATCH and a separate input type with pointer fields.
func (c *Client) UpdatePost(ctx context.Context, id int, input PostInput) (Post, error) {
	if id <= 0 {
		return Post{}, fmt.Errorf("post id must be greater than zero")
	}

	var updated Post
	path := fmt.Sprintf("/posts/%d", id)
	if err := c.do(ctx, http.MethodPut, path, input, &updated); err != nil {
		return Post{}, fmt.Errorf("update post %d: %w", id, err)
	}
	return updated, nil
}

// DeletePost removes a post with DELETE /posts/{id}.
//
// JSONPlaceholder simulates deletion: it returns a success response even though
// the resource still exists on the server.
func (c *Client) DeletePost(ctx context.Context, id int) error {
	if id <= 0 {
		return fmt.Errorf("post id must be greater than zero")
	}

	path := fmt.Sprintf("/posts/%d", id)
	if err := c.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return fmt.Errorf("delete post %d: %w", id, err)
	}
	return nil
}

// do performs one request and decodes the response into responseBody.
//
// A nil requestBody sends no body. A nil responseBody ignores the response body.
func (c *Client) do(ctx context.Context, method, path string, requestBody, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer response.Body.Close()

	// Read at most maxResponseBytes+1 bytes so a huge response cannot consume
	// unbounded memory, while still detecting that the limit was exceeded.
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if len(payload) > maxResponseBytes {
		return fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &HTTPError{
			Method:     method,
			Path:       path,
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Body:       truncate(strings.TrimSpace(string(payload)), maxErrorBodyRunes),
		}
	}

	if responseBody == nil {
		return nil
	}

	// Some successful endpoints may return an empty body. Treat that as an
	// error only when the caller expects a payload to decode.
	if len(bytes.TrimSpace(payload)) == 0 {
		return fmt.Errorf("decode response: empty body")
	}
	if err := json.Unmarshal(payload, responseBody); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// truncate shortens s to at most max runes without splitting a UTF-8 sequence.
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}
