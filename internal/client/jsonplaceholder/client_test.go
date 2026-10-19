package jsonplaceholder_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/taskflow/internal/client/jsonplaceholder"
)

// newTestClient starts a local HTTP server and returns a client pointed at it.
// This keeps the tests fast and independent of the public internet.
func newTestClient(t *testing.T, handler http.HandlerFunc) *jsonplaceholder.Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := jsonplaceholder.New(server.URL, nil)
	if err != nil {
		t.Fatalf("New() returned an unexpected error: %v", err)
	}
	return client
}

func TestGetPost(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want %s", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/posts/1" {
			t.Errorf("path = %s, want %s", r.URL.Path, "/posts/1")
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("User-Agent header is missing")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"userId": 1,
			"id": 1,
			"title": "sunt aut facere",
			"body": "quia et suscipit"
		}`)
	})

	post, err := client.GetPost(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetPost() returned an unexpected error: %v", err)
	}

	if post.ID != 1 || post.UserID != 1 {
		t.Errorf("post IDs = user %d / post %d, want 1 / 1", post.UserID, post.ID)
	}
	if post.Title != "sunt aut facere" {
		t.Errorf("Title = %q, want %q", post.Title, "sunt aut facere")
	}
}

func TestCreatePost(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want %s", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/posts" {
			t.Errorf("path = %s, want %s", r.URL.Path, "/posts")
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}

		var input jsonplaceholder.PostInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		if input.Title != "New post" {
			t.Errorf("request title = %q, want %q", input.Title, "New post")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"userId": 1,
			"id": 101,
			"title": "New post",
			"body": "Created by the test."
		}`)
	})

	created, err := client.CreatePost(context.Background(), jsonplaceholder.PostInput{
		UserID: 1,
		Title:  "New post",
		Body:   "Created by the test.",
	})
	if err != nil {
		t.Fatalf("CreatePost() returned an unexpected error: %v", err)
	}

	// JSONPlaceholder assigns the ID; the caller must use the response value.
	if created.ID != 101 {
		t.Errorf("created ID = %d, want 101", created.ID)
	}
	if created.Title != "New post" {
		t.Errorf("created Title = %q, want %q", created.Title, "New post")
	}
}

func TestUpdatePost(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want %s", r.Method, http.MethodPut)
		}
		if r.URL.Path != "/posts/101" {
			t.Errorf("path = %s, want %s", r.URL.Path, "/posts/101")
		}

		var input jsonplaceholder.PostInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"userId": 1,
			"id": 101,
			"title": "Updated post",
			"body": "PUT replaces the full post."
		}`)
	})

	updated, err := client.UpdatePost(context.Background(), 101, jsonplaceholder.PostInput{
		UserID: 1,
		Title:  "Updated post",
		Body:   "PUT replaces the full post.",
	})
	if err != nil {
		t.Fatalf("UpdatePost() returned an unexpected error: %v", err)
	}
	if updated.Title != "Updated post" {
		t.Errorf("updated Title = %q, want %q", updated.Title, "Updated post")
	}
}

func TestDeletePost(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want %s", r.Method, http.MethodDelete)
		}
		if r.URL.Path != "/posts/101" {
			t.Errorf("path = %s, want %s", r.URL.Path, "/posts/101")
		}

		// JSONPlaceholder returns 200 with an empty JSON object for DELETE.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})

	if err := client.DeletePost(context.Background(), 101); err != nil {
		t.Fatalf("DeletePost() returned an unexpected error: %v", err)
	}
}

func TestHTTPError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"post not found"}`)
	})

	_, err := client.GetPost(context.Background(), 999)
	if err == nil {
		t.Fatal("GetPost() error = nil, want an HTTPError")
	}

	// Callers can inspect the remote status without parsing an error string.
	var httpError *jsonplaceholder.HTTPError
	if !errors.As(err, &httpError) {
		t.Fatalf("error type = %T, want *jsonplaceholder.HTTPError", err)
	}
	if httpError.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", httpError.StatusCode, http.StatusNotFound)
	}
	if httpError.Method != http.MethodGet {
		t.Errorf("Method = %q, want %q", httpError.Method, http.MethodGet)
	}
	if httpError.Path != "/posts/999" {
		t.Errorf("Path = %q, want %q", httpError.Path, "/posts/999")
	}
	if !strings.Contains(httpError.Body, "post not found") {
		t.Errorf("Body = %q, want it to contain the remote error", httpError.Body)
	}
}

func TestNewRejectsInvalidBaseURL(t *testing.T) {
	tests := []string{
		"",
		"jsonplaceholder.typicode.com",
		"ftp://jsonplaceholder.typicode.com",
		"https://",
		"https://jsonplaceholder.typicode.com?token=secret",
	}

	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := jsonplaceholder.New(rawURL, nil); err == nil {
				t.Fatalf("New(%q) error = nil, want a validation error", rawURL)
			}
		})
	}
}

func TestClientRespectsCanceledContext(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// This handler would normally respond; the request should never reach it
		// because the context is already canceled.
		w.WriteHeader(http.StatusInternalServerError)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetPost(ctx, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetPost() error = %v, want context.Canceled", err)
	}
}
