---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
description: "Run Delivery Promise locally against real Postgres and the platform Kafka broker, and make the first REST, MCP and A2A calls."
---

# Quickstart

Delivery Promise runs on real PostgreSQL and real Kafka only: there is no in-memory mode, no log sink and no docker-compose file (the compose Kafka is retired; the platform has **one** Kafka broker, reachable as `localhost:9092` from the kind cluster). The embedded migrations run at boot.

## Prerequisites

| Tool | Why |
| --- | --- |
| Go 1.27.x (`go.mod`) | build and run the three binaries |
| Docker | a local Postgres, and the testcontainers-based `make integration` |
| The kind cluster `warehouse` with its Kafka broker on `localhost:9092` | the one Kafka of the platform. Any broker reachable at a `host:port` works for a pure local try-out |
| `golangci-lint` v2.14.0, `gremlins` v0.6.0, `govulncheck` (optional) | only for `make lint`, `make mutation`, `make vuln` |
| Node 20+ (optional) | only for `web/` (the console remote) and for this documentation site in `docs/` |

## Make targets

`make help` lists them. The ones you will use (all from the `Makefile`):

| Target | What it runs |
| --- | --- |
| `make build` | `go build ./...` |
| `make test` | `go test ./... -race` (unit, handler and godog tests, no Docker) |
| `make check` | `fmt-check vet build lint test`, the pre-push gate |
| `make check-all` | `check` plus `coverage` (90 % gate), `arch-test`, `bdd` |
| `make integration` | `go test -tags=integration ./... -race -count=1` (testcontainers boots Postgres and Kafka; needs only Docker) |
| `make arch-test`, `make bdd` | fitness tests, godog features |
| `make mutation` | gremlins once per `internal/domain/<pkg>` |

## 1. Start Postgres

```sh
docker run -d --name dp-pg -e POSTGRES_USER=dp -e POSTGRES_PASSWORD=dp \
  -e POSTGRES_DB=delivery_promise -p 55441:5432 postgres:16
```

## 2. Start the three binaries

Each in its own terminal (the ports below override the defaults `:8080`, `:8090` and `:8093` only because those were taken on the machine where this page was captured).

```sh
export DATABASE_URL='postgres://dp:dp@localhost:55441/delivery_promise?sslmode=disable'

# REST API + consumer + sweeper + outbox relay (needs Kafka)
KAFKA_BROKERS=localhost:9092 DELIVERY_PROMISE_CONSUMER_GROUP=delivery-promise-docs-quickstart \
  HTTP_ADDR=:18080 go run ./cmd/api

# read-only MCP server (Postgres only)
MCP_ADDR=:18090 go run ./cmd/mcp

# read-only A2A agent (Postgres only)
A2A_ADDR=:18093 A2A_AGENT_URL=http://localhost:18093/ go run ./cmd/a2a
```

`DATABASE_URL`, `KAFKA_BROKERS` and `DELIVERY_PROMISE_CONSUMER_GROUP` are mandatory for `cmd/api`; `cmd/a2a` also needs `A2A_AGENT_URL`. A missing one is a boot error naming it, see [Configuration](/docs/operations/configuration).

With a fresh consumer group the consumer starts at the beginning of the eight producer topics and fills the local read models (`rm_*` tables). On the captured run that was 2,719 events (`processed_events`), giving 43 nodes, 40 departures and 19 delivery waves, enough to quote immediately. If the producers have written nothing yet, the read models are empty and a quote returns no offers (a normal 200 with the tiers under `unavailable`) until their events arrive.

## 3. First calls (captured from a real run)

Probes:

```text
$ curl -s -i localhost:18080/readyz
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ready"}

$ curl -s -i localhost:18080/healthz
HTTP/1.1 200 OK

{"status":"ok"}
```

The built-in policy `v0` before anything is published:

```text
$ curl -s localhost:18080/promise-policies/current
{"policyVersion":"v0","version":0,"offerTtlMinutes":10,"holdTtlMinutes":15,"packagesPerOrder":1,"capacitySharePercent":100,"orderDeadlines":[],"publishedAt":"0001-01-01T00:00:00Z"}
```

Publish a policy (`Idempotency-Key` is required):

```text
$ curl -s -i -X POST localhost:18080/promise-policies \
    -H 'Content-Type: application/json' -H 'Idempotency-Key: quickstart-policy-1' \
    -d '{"offerTtlMinutes":10,"holdTtlMinutes":15,"packagesPerOrder":1,"capacitySharePercent":100,"orderDeadlines":[{"tier":"NEXT_DAY","localTime":"18:00"}]}'
HTTP/1.1 201 Created

{"policyVersion":"v1","version":1,"offerTtlMinutes":10,"holdTtlMinutes":15,"packagesPerOrder":1,"capacitySharePercent":100,"orderDeadlines":[{"tier":"NEXT_DAY","localTime":"18:00"}],"publishedAt":"2026-10-31T16:43:38.171412Z"}
```

Quote a delivery offer for a catalogue item known to the read models (the `catalogItemId` came from the `rm_listings` table the consumer filled):

```text
$ curl -s -X POST localhost:18080/delivery-offers:quote -H 'Content-Type: application/json' \
    -d '{"destinationPostalPrefix":"787","lines":[{"catalogItemId":"item-4749b893-2ad9-450c-bd4e-b992c15b4e8a","quantity":1}],"tiers":["STANDARD"]}'
{"offers":[{"offerId":"off-6ed676d4-f7ff-4518-80a5-ba623b55afa5","state":"QUOTED","groupNo":1,"destinationPostalPrefix":"787","tier":"STANDARD","lines":[{"catalogItemId":"item-4749b893-2ad9-450c-bd4e-b992c15b4e8a","quantity":1}],"window":{"earliest":"2026-11-01T15:00:00Z","latest":"2026-11-02T03:00:00Z","timeZone":"America/Chicago"},"witnessPath":{"nodeId":"node-98ff00c3-e4e0-454c-946d-cd58721d7107","pickupDepartureId":"dep-0219ff091eaf4cf9c7f93f27b2a17c5b","legs":[],"deliveryWaveId":"wave-d74aea0d-6d91-48ea-ae05-10d939ebad59"},"basis":"SCHEDULED","policyVersion":"v1","quotedAt":"2026-10-31T16:44:30.812774Z","expiresAt":"2026-10-31T16:54:30.812774Z"}],"unavailable":[]}
```

Make a PROVISIONAL promise from that offer (the body must repeat the offer's lines):

```text
$ curl -s -i -X POST localhost:18080/promises -H 'Content-Type: application/json' -H 'Idempotency-Key: quickstart-make-2' \
    -d '{"offerId":"off-6ed676d4-f7ff-4518-80a5-ba623b55afa5","lines":[{"catalogItemId":"item-4749b893-2ad9-450c-bd4e-b992c15b4e8a","quantity":1}]}'
HTTP/1.1 201 Created

{"promiseId":"prm-c3b22aa8-be97-4646-a3ae-fe08e21295c2","offerId":"off-6ed676d4-f7ff-4518-80a5-ba623b55afa5","state":"PROVISIONAL","destinationPostalPrefix":"787","lines":[{"catalogItemId":"item-4749b893-2ad9-450c-bd4e-b992c15b4e8a","quantity":1}],"terms":{"tier":"STANDARD","shipBy":"2026-10-31T22:00:00Z","deliverByEarliest":"2026-11-01T15:00:00Z","deliverByLatest":"2026-11-02T03:00:00Z","timeZone":"America/Chicago","basis":"SCHEDULED","policyVersion":"v1"},"witnessPath":{"nodeId":"node-98ff00c3-e4e0-454c-946d-cd58721d7107","pickupDepartureId":"dep-0219ff091eaf4cf9c7f93f27b2a17c5b","legs":[],"deliveryWaveId":"wave-d74aea0d-6d91-48ea-ae05-10d939ebad59"},"holdExpiresAt":"2026-10-31T16:59:59.208433Z","revisions":[],"version":1}
```

The hold shows on the node's book (the cutoff of the witness pickup departure):

```text
$ curl -s "localhost:18080/promise-books?nodeId=node-98ff00c3-e4e0-454c-946d-cd58721d7107&date=2026-10-31"
{"items":[{"bookId":"node-98ff00c3-e4e0-454c-946d-cd58721d7107:dep-0219ff091eaf4cf9c7f93f27b2a17c5b","nodeId":"node-98ff00c3-e4e0-454c-946d-cd58721d7107","pickupDepartureId":"dep-0219ff091eaf4cf9c7f93f27b2a17c5b","cutoffAt":"2026-10-31T22:00:00Z","state":"OPEN","capacity":{"share":250,"provisional":1,"confirmed":0},"version":2}]}
```

Errors are `application/problem+json`:

```text
$ curl -s -i localhost:18080/delivery-offers/offer-nope
HTTP/1.1 404 Not Found
Content-Type: application/problem+json

{"type":"https://retailnetwork.example/problems/not-found","title":"Not found","status":404,"detail":"not found","instance":"/delivery-offers/offer-nope"}
```

The outbox row of the policy was relayed on the first attempt:

```text
event_type                                                                       | topic                                    | attempts | published
com.retailnetwork.commerce.delivery-promise.promisepolicy.PromisePolicyPublished | retailnetwork.delivery-promise.analytics |        1 | t
```

### MCP (Streamable HTTP, read-only)

`initialize` returns a session id in the `Mcp-Session-Id` header; send it on every later call. The server name is `delivery-promise-mcp`, protocol `2025-03-26`.

```sh
curl -s -i -X POST localhost:18090/mcp -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
# -> HTTP/1.1 200 OK, Mcp-Session-Id: ID7QUE6BGSJ3AUNN7KL6VPURSV, capabilities {"logging":{},"tools":{"listChanged":true}}

curl -s -X POST localhost:18090/mcp -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' -H 'Mcp-Session-Id: ID7QUE6BGSJ3AUNN7KL6VPURSV' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_current_policy","arguments":{}}}'
# -> structuredContent {"capacitySharePercent":100,"holdTtlMinutes":15,"offerTtlMinutes":10,"orderDeadlines":[{"localTime":"18:00","tier":"NEXT_DAY"}],"packagesPerOrder":1,"policyVersion":"v1","publishedAt":"2026-10-31T16:43:38.171412Z","version":1}
```

All five tools are listed in [MCP tools](/docs/mcp/tools).

### A2A (JSON-RPC 2.0, read-only)

The Agent Card is at `/.well-known/agent-card.json`; requests go to `/` or `/a2a` and **must send `A2A-Version: 1.0`** (a client asking for `0.3` gets error `-32009 VERSION_NOT_SUPPORTED`).

```text
$ curl -s -X POST localhost:18093/a2a -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' \
    -d '{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"book_capacity","arguments":{"nodeId":"node-98ff00c3-e4e0-454c-946d-cd58721d7107","date":"2026-10-31"}}}]}}}'
{"jsonrpc":"2.0","id":1,"result":{"message":{...,"parts":[{"data":{"skill":"book_capacity","data":{"bookCapacity":{"nodeId":"node-98ff00c3-...","date":"2026-10-31","books":[{...,"state":"OPEN","capacity":{"share":250,"provisional":1,"confirmed":0},"version":2}]}}}},{"text":"node node-98ff00c3-... has 1 promise book(s) on 2026-10-31: ... OPEN share 250, provisional 1, confirmed 0"}],"role":"ROLE_AGENT"}}}
```

See [API reference](/docs/api-reference) for the full surface.

## 4. Tests and cleanup

```sh
make check          # fmt-check, vet, build, lint, test
make integration    # needs Docker only
docker rm -f dp-pg  # remove the database
```

Next: [Configuration](/docs/operations/configuration), [Runbook](/docs/operations/runbook), [Testing](/docs/development/testing).
