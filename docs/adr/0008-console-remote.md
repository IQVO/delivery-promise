# ADR 0008: a warehouse-console web remote

## Status

Accepted (2026-10-24).

## Context

The fleet's operators work in one warehouse-console SPA (a Module Federation host) that lazy-loads one remote per bounded context; task-interleaving, slotting-optimization and pick-face-replenishment already ship theirs. delivery-promise had no console surface at all: its answers were reachable only through the REST API or the MCP tools. Everything an operator needs day to day, though, is a read: the promises of an order, one promise's terms and judgement, the offer a shopper saw, how full a node's books are, and the policy in force.

## Decision

- **A read-only Module Federation remote, `web/`.** Container name `delivery_promise_mfe`, exposing exactly `./App` (default export, no props). The console lazy-loads it as `delivery_promise_mfe/App` and mounts it under its own `/delivery-promise/*` splat route; `main.tsx` keeps a standalone dev entry on port **4013** (the fleet's 5181-5197 ladder and 4010/4011 are taken; `strictPort`). The production asset base is `/mfes/delivery-promise/`, the Nginx web gateway path in warehouse-infra.
- **Four screens over the five GET reads, nothing else.** Promises (by order, with a detail screen: terms, witness path, revisions, judgement), Offers (by id: tier, window, witness path), Books (a node and a date: the capacity ledger with share and holds) and Policy (the current version with its order deadlines). Every call goes to this context's own API only: `api.ts` builds the base from the shell's runtime `/config.json` (`window.__WAREHOUSE_CONFIG__.apiOrigin`) plus `/api/delivery-promise`, and throws in production rather than falling back to a developer port. There is no write screen, no POST, no `If-Match`: quoting and the promise lifecycle belong to checkout and REST.
- **Built and deployed like the other remotes.** Vite config in object form (a callback export cannot be merged by `mergeConfig`, which has killed test suites in the fleet); `@warehouse/ui-kit` consumed as the `file:../../warehouse-ui-kit` sibling (built first as a named BuildKit context in the two-stage Dockerfile); the runtime image is nginx-unprivileged serving the hashed assets and `/healthz`, never routing to an API. npm runs only inside `web/`, never at the repo root.
- **Chart and CI.** The chart gains a `frontend` Deployment + ClusterIP Service, default OFF, never routed by this chart (the web gateway owns the path); `ci/all-components-values.yaml` renders it with everything else. The `web` CI job checks out this repo plus `IQVO/warehouse-ui-kit@develop`, builds the kit, then runs `npm ci`, oxlint, `tsc -b`, vitest and `vite build`.

## Consequences

- An operator can trace a shopper's promise end to end without curl: order to promises to the witness cutoff that backs them, plus the books and the policy.
- The console contract (container name, exposed module, base path, port) is pinned here; changing any of it is a coordinated change with warehouse-console and warehouse-infra.
- The remote stays read-only by construction: there is no write path in `api.ts` to accidentally wire a button to.
