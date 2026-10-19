// Package redact provides small helpers for logging connection targets without
// leaking credentials.
//
// Startup logs are intentionally verbose in this project, which makes it very
// easy to accidentally print a database password. Every connection package
// routes its target through RedactURL before logging it.
package redact

import "net/url"

// URL removes the password from a URL while keeping the username, host, path,
// and query string visible. That is enough to diagnose "wrong host" or "wrong
// database" problems without exposing a secret.
//
// Invalid URLs are replaced with a fixed placeholder rather than echoed back,
// because the invalid string may itself contain a password.
func URL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.User(parsed.User.Username())
		}
	}
	return parsed.String()
}
