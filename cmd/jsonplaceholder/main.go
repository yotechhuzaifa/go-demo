// Command jsonplaceholder demonstrates calling a third-party REST API.
//
// It performs the four operations requested by the demo:
//
//	GET    /posts/1
//	POST   /posts
//	PUT    /posts/{id}
//	DELETE /posts/{id}
//
// JSONPlaceholder is a fake API, so writes are simulated and nothing is
// permanently changed on the server.
//
// Run it with:
//
//	go run ./cmd/jsonplaceholder
//	make demo-jsonplaceholder
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/taskflow/internal/client/jsonplaceholder"
)

func main() {
	// main stays small: report errors and choose the process exit code.
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "jsonplaceholder demo: %v\n", err)
		os.Exit(1)
	}
}

// run parses flags, builds the client, and executes the demo calls.
func run() error {
	// Allow the endpoint to be overridden for local mocks or proxies. The flag
	// is more discoverable than an environment variable, but the environment
	// variable is useful in CI.
	defaultBaseURL := os.Getenv("JSONPLACEHOLDER_BASE_URL")
	if defaultBaseURL == "" {
		defaultBaseURL = jsonplaceholder.DefaultBaseURL
	}

	baseURL := flag.String("base-url", defaultBaseURL, "JSONPlaceholder-compatible base URL")
	timeout := flag.Duration("timeout", 10*time.Second, "timeout for the entire HTTP exchange")
	flag.Parse()

	// HTTP clients are safe for concurrent use and should be reused. A timeout
	// here covers connection setup, redirects, and reading the response body.
	httpClient := &http.Client{Timeout: *timeout}
	client, err := jsonplaceholder.New(*baseURL, httpClient)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	// Cancel outstanding calls on Ctrl+C or SIGTERM. The client passes this
	// context into every outgoing request.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Using API: %s\n\n", *baseURL)

	// 1. GET ---------------------------------------------------------------
	fmt.Println("1. GET /posts/1")
	post, err := client.GetPost(ctx, 1)
	if err != nil {
		return err
	}
	printJSON("Response:", post)
	fmt.Println()

	// 2. POST --------------------------------------------------------------
	fmt.Println("2. POST /posts")
	created, err := client.CreatePost(ctx, jsonplaceholder.PostInput{
		UserID: 1,
		Title:  "Learning Go HTTP clients",
		Body:   "This post was created by cmd/jsonplaceholder.",
	})
	if err != nil {
		return err
	}
	printJSON("Response:", created)
	fmt.Println("Note: JSONPlaceholder does not persist created posts, so the")
	fmt.Println("update and delete calls below target the existing post fetched above.")
	fmt.Println()

	// 3. PUT ---------------------------------------------------------------
	// PUT replaces the whole resource, so all writable fields are supplied.
	// The ID comes from the GET response because writes are not persisted by
	// JSONPlaceholder; using the simulated POST ID would produce a server error.
	fmt.Printf("3. PUT /posts/%d\n", post.ID)
	updated, err := client.UpdatePost(ctx, post.ID, jsonplaceholder.PostInput{
		UserID: post.UserID,
		Title:  "Learning Go HTTP clients (updated)",
		Body:   "PUT replaces the full post.",
	})
	if err != nil {
		return err
	}
	printJSON("Response:", updated)
	fmt.Println()

	// 4. DELETE ------------------------------------------------------------
	fmt.Printf("4. DELETE /posts/%d\n", post.ID)
	if err := client.DeletePost(ctx, post.ID); err != nil {
		return err
	}
	fmt.Println("Delete succeeded.")
	fmt.Println("Note: JSONPlaceholder simulates writes; the post is not really removed.")
	return nil
}

// printJSON renders a value with indentation so the demo output is easy to
// compare with the API documentation.
func printJSON(label string, value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		// Marshaling these fixed structs cannot realistically fail. Printing the
		// error instead of panicking keeps the demo resilient anyway.
		fmt.Printf("%s <failed to encode: %v>\n", label, err)
		return
	}
	fmt.Printf("%s\n%s\n", label, encoded)
}
