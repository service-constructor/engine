# Service Constructor

Open-source platform for embedding mini-apps and a payment saga into any
fintech wallet that handles money. Based on the
[Service Constructor white paper](./Service_Constructor_Whitepaper.pdf).

A registered **service** is an autonomous web app the wallet embeds via a
WebView; settlement runs through an orchestrator that freezes funds, executes
the service, and captures payment under a transactional saga.

This repository currently implements the **Service Registry** — CRUD over
services — exposed as a gRPC API with an HTTP/JSON gateway in front of it. The
payment saga is the next milestone.

## Architecture

```
HTTP/JSON  ──►  grpc-gateway  ──►  gRPC server  ──►  Registry use case  ──►  Postgres
  (:8080)        (reverse proxy)     (:9090)          (validation, ids)        (pgx)
```

Layers (clean separation, transport- and storage-agnostic core):

| Path                              | Role                                              |
|-----------------------------------|---------------------------------------------------|
| `proto/`                          | gRPC + HTTP contract (source of truth)            |
| `gen/`                            | Generated stubs (`buf generate`)                  |
| `internal/domain`                 | Core entities and invariants, no deps             |
| `internal/service`               | Use cases (`Registry`), `Repository` port         |
| `internal/repository/postgres`    | Postgres adapter (pgx), migrations runner         |
| `internal/server`                 | gRPC adapter, proto↔domain mapping                |
| `cmd/server`                      | Wiring: migrations, gRPC, gateway, shutdown       |

## Quick start

```bash
# 1. Start Postgres
make docker-up

# 2. Run the server (applies migrations on startup)
make run
```

Defaults (override via env): `GRPC_ADDR=:9090`, `HTTP_ADDR=:8080`,
`DATABASE_URL=postgres://sc:sc@localhost:5432/service_constructor?sslmode=disable`.

## REST API (via gateway)

| Method & path                          | Purpose          |
|----------------------------------------|------------------|
| `POST   /v1/admin/services`            | Create a service |
| `GET    /v1/admin/services/{id}`       | Get a service    |
| `GET    /v1/admin/services`            | List (paginated) |
| `PATCH  /v1/admin/services/{id}`       | Partial update   |
| `DELETE /v1/admin/services/{id}`       | Delete           |

List supports `pageSize`, `pageToken` (keyset cursor) and `status` filter.
`PATCH` uses field-mask semantics: only fields present in the JSON body change.

Example:

```bash
curl -X POST localhost:8080/v1/admin/services -H 'Content-Type: application/json' -d '{
  "name": "eSIM Provider",
  "origins": ["https://esim.example.com"],
  "executeUrl": "https://esim.example.com/execute",
  "statusUrl": "https://esim.example.com/status",
  "receivingWallets": [{"currencyId": "1", "walletId": "wlt_usdt_01"}],
  "fee": {"percent": "1.5"},
  "status": "SERVICE_STATUS_ACTIVE"
}'
```

## Development

```bash
make tools      # install protoc-gen-go, -go-grpc, -grpc-gateway
make generate   # regenerate gen/ from proto/
make test       # run unit tests
make build      # build ./bin/server
```

Code generation uses [buf](https://buf.build) with googleapis annotation protos
vendored under `third_party/` (no Buf Schema Registry auth required).

## Roadmap

- [x] Service Registry CRUD (gRPC + HTTP gateway, Postgres)
- [ ] Signed quote + device-signed consent verification
- [ ] Payment saga: `freeze → execute → capture` with compensation
- [ ] Reconciler (query-before-compensate) and outbox dispatcher
- [ ] Server SDK for service integrators
