# Multi-stage build.
#
# Stage 1 compiles a static Linux binary. Stage 2 contains only that binary, so
# the runtime image has no compiler, shell, or package manager to exploit.
# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build

# Keep the build directory predictable and outside GOPATH.
WORKDIR /src

# Copy dependency metadata first. Docker can cache this layer until go.mod or
# go.sum changes, which makes rebuilds faster.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source and compile.
COPY . .

# VERSION is supplied by the Makefile or CI:
#   docker build --build-arg VERSION=1.2.3 .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/taskflow \
    ./cmd/api

# Runtime stage. distroless includes CA certificates and timezone data but no
# shell. "nonroot" runs the process as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot

# Copy only the compiled artifact from the build stage.
COPY --from=build /out/taskflow /taskflow

# Document the listening port. EXPOSE is metadata; the app reads HTTP_ADDR.
EXPOSE 8080

# The image already runs as nonroot; this repeats the intent explicitly.
USER nonroot:nonroot

ENTRYPOINT ["/taskflow"]
