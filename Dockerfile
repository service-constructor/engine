#
# Build context is the PARENT of engine/ and ledger/ (i.e. the dir containing
# both), because engine/go.mod has `replace github.com/nvsces/ledger => ../ledger`
# and needs the sibling ledger module present at build time.
#
#   docker build -f engine/Dockerfile -t serviceconstructor-engine:latest .
#
FROM golang:1.25-alpine AS build
WORKDIR /src

# ledger is a path-replaced dependency of engine — copy it first so module
# resolution works, then engine on top.
COPY ledger/ ./ledger/
COPY engine/ ./engine/

WORKDIR /src/engine
RUN go mod download

# Static, CGO-free build (pgx is pure Go).
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/engine ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/engine /engine
# gRPC :9090, HTTP gateway :8080 (see internal/config/config.go defaults).
EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/engine"]
