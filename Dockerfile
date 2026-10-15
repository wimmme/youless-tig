# syntax=docker/dockerfile:1
#
# youless-tig: one static Go binary on Alpine.
#
#   docker build -t youless-tig .                  production image
#   docker build --target test .                   all checks (what CI runs)
#   docker build --target sim -t youless-sim .     the simulated YouLess (tests, demos)
#
# The Go stages run on the BUILD platform and cross-compile, so a multi-arch
# build emulates nothing except the few RUN lines of the final stage.

# ── sources ──────────────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS source
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .

# ── checks: gofmt, vet, tests with the race detector (needs cgo → Debian image) ─
FROM golang:1.27 AS test
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN unformatted="$(gofmt -l .)"; if [ -n "$unformatted" ]; then echo "gofmt needed:"; echo "$unformatted"; exit 1; fi
RUN go vet ./... && go vet -tags integration ./...
RUN go test -race -count=1 ./...

# ── binaries ─────────────────────────────────────────────────────────────────
FROM source AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags="-s -w -X github.com/wimmme/youless-tig/internal/version.Version=${VERSION} -X github.com/wimmme/youless-tig/internal/version.Commit=${COMMIT}" \
      -o /out/youless-tig ./cmd/youless-tig \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" \
      -o /out/youless-sim ./cmd/youless-sim

# ── simulated YouLess (not published) ────────────────────────────────────────
FROM alpine:3.24 AS sim
RUN adduser -D -H -u 10001 youless
COPY --from=build /out/youless-sim /usr/local/bin/youless-sim
USER youless
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/youless-sim", "-listen", ":8081"]

# ── runtime ──────────────────────────────────────────────────────────────────
FROM alpine:3.24
# ca-certificates: InfluxDB over HTTPS. Time zones are built into the binary.
RUN apk add --no-cache ca-certificates \
 && adduser -D -H -u 10001 youless
COPY --from=build /out/youless-tig /usr/local/bin/youless-tig
USER youless
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD ["/usr/local/bin/youless-tig", "-healthcheck"]
ENTRYPOINT ["/usr/local/bin/youless-tig"]
