// Module path used for all imports inside this project.
//
// In a real repository this would usually be the VCS URL, for example:
//
//	module github.com/your-org/taskflow
//
// "example.com" keeps this sample self-contained while still demonstrating
// the recommended convention of using a globally unique module path.
module example.com/taskflow

// Go version used by this project. The code relies on:
//   - net/http method + wildcard routing (Go 1.22)
//   - log/slog structured logging (Go 1.21)
//   - built-in min() (Go 1.21)
//
// Dependencies are pinned to versions that still support Go 1.24.
go 1.24.0

require (
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de
	github.com/avast/retry-go/v4 v4.6.1
	github.com/dustin/go-humanize v1.0.1
	github.com/go-playground/validator/v10 v10.26.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.6
	github.com/joho/godotenv v1.5.1
	github.com/lmittmann/tint v1.2.1
	github.com/pressly/goose/v3 v3.24.1
	github.com/redis/go-redis/v9 v9.7.3
	github.com/swaggo/http-swagger/v2 v2.0.2
	github.com/swaggo/swag v1.16.6
	go.mongodb.org/mongo-driver v1.17.10
	golang.org/x/sync v0.13.0
)

require (
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/gabriel-vasile/mimetype v1.4.8 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.20.0 // indirect
	github.com/go-openapi/spec v0.20.6 // indirect
	github.com/go-openapi/swag v0.19.15 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/compress v1.17.7 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mailru/easyjson v0.7.6 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/montanaflynn/stats v0.7.1 // indirect
	github.com/rogpeppe/go-internal v1.13.1 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	github.com/swaggo/files/v2 v2.0.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/mod v0.18.0 // indirect
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	golang.org/x/tools v0.22.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
