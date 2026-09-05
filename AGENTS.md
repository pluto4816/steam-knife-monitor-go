# AGENTS.md

## Source of truth

Before changing code or tests, read `GO_V1_SPEC.md` and `GO_V2_SPEC.md`. V1 remains the source
of truth for existing business rules and production behavior. V2 defines only the explicitly
started extension phase.

The sibling Python repository is frozen reference material only. It may be read for verified
requirements, API findings, and test cases, but must not be modified unless the user explicitly
asks.

## Product constraints

The production target remains one server, one process, one user, one notification channel,
SQLite, and one Go executable. QQ official bot C2C messaging is the only notification channel.

Prefer standard-library functions and concrete types. Add an interface only when there are two
current implementations or when testing an external side effect clearly requires one.

Do not overdesign. Work one phase at a time, implement only the current specification, and do
not introduce microservices, queues, worker pools, leases, distributed coordination, an ORM, a
web framework, dependency-injection framework, dynamic plugin system, event bus, or speculative
abstractions. The V2.2 candidate-source collector is an isolated built-in manual mode, not a
general plugin framework.

Ratio business logic must use `core.RatioBP`; never compare ratios through `float64`. Prices
should remain exact strings until the real API representation is confirmed.

## Data access

Use only the smis.club endpoints and fields already verified in the frozen Python findings.
V2.2 may also read only the ByMykel CSGO-API flat English skin-definition snapshot and fields
documented in `GO_V2_SPEC.md`. V2.3 Phase 1 may use only the documented C5GAME OpenAPI balance,
quick-buy, and order-detail endpoints plus Steam Community Market's Steam-hosted
`/market/priceoverview/` endpoint exactly as documented in the V2.3 specification. Treat the
definition snapshot as mutable input derived from CS2 game definitions, not as Steam
market-state evidence.

Favor low-frequency, sequential, batched, and cached access. Do not bypass access controls,
CAPTCHA, Cloudflare, rate limits, or other site restrictions. Stop or back off on empty
responses, 401, 403, 429, challenges, and similar restrictions.

The ByMykel snapshot collector runs only in explicit `update-candidates` mode. Normal modes read
the persisted catalog, make no ByMykel requests, and make no Steam Community Market requests
during targeted full scans or due checks. They must not paginate the global smis.club exchange
endpoint. They fetch only verified smis.club single-item details for catalog IDs. The sole Steam
Community Market exception is the final C5 purchase-confirmation path described below;
`/market/priceoverview/` must never be used for candidate collection, global scanning, ordinary
tier persistence, or due-check processing.

Absence is not market-state evidence. Only successfully parsed and validated MarketData may
change a tier. A failed candidate update must preserve the prior valid catalog.

Targeted full scans persist a single-process checkpoint only after an item's validated state is
saved. A retry may resume only when the checkpoint matches the exact ordered candidate catalog;
catalog changes invalidate old progress. An incomplete scan must still perform no due-check or
notification phase.

A successful targeted full scan also persists its catalog fingerprint, candidate count, and
completion time. On `monitor` restart only, the initial full scan may be skipped when that
durable completion is less than 24 hours old, its fingerprint and count match the exact current
catalog, every catalog identity has exactly one persisted state, and no checkpoint exists.
The next 24-hour full scan remains anchored to the persisted completion time. A failed check
must perform the normal full scan; `once` and `preview` never use this restart shortcut.

## C5 purchase price-confirmation flow

The payment path is fixed and must remain in this order:

```text
SMIS scans the 426 persisted candidates, or a restarting monitor verifies the durable recent
  full-scan completion and all 426 persisted identities
→ run due checks using SMIS and use SMIS transaction volume and prices for preliminary
  classification
→ obtain current A-tier candidates
→ in the purchase phase, refresh SMIS detail and require C5 to be the lowest positive platform
→ require the exact C5 price to be no greater than CNY 1.00
→ only for a candidate that could now actually be purchased, call Steam Community Market
  `/market/priceoverview/` once for its exact `market_hash_name`
→ use Steam `lowest_price` to recompute `core.RatioBP` exactly
→ require the recomputed evidence to satisfy the base A rule without hysteresis
→ in live mode, query the C5 balance
→ durably reserve the item and budget, then submit the C5 quick-buy request
```

`/market/priceoverview/` is a payment-confirmation input only. Targeted full scans and due checks
must not call it; they continue to use validated smis.club `steamSellPrice` and
`steamTransactionQuantity`. A current A-tier item that is not C5-lowest, has a C5 price above
CNY 1.00, has already been handled, or otherwise cannot be purchased must be excluded before any
Steam price request. Dry-run follows the same prefilter and Steam reprice decision but performs
no C5 balance query, ledger mutation, or quick-buy.

Steam price failure, absence, invalid currency, restriction, or a recomputed non-A result must
prevent purchase. Steam transaction volume remains a smis.club screening input and is never a
payment amount. Do not restore global Steam Community Market search or pagination.

## Manual specified-item purchase test

The explicit `purchase-test` mode is a narrow exception to the automatic price-confirmation
flow above. It selects exactly one existing catalog item by `item_id`, runs once, and exits.
It does not call SMIS or Steam, does not require persisted SMIS identity or tier state, and does
not compare C5 with other platforms. Its sole market-price eligibility rule is a C5 quick-buy
`maxPrice` fixed at CNY 1.00; a successful `actualPay` must also be positive and no greater than
that ceiling.

This mode is live-only and must retain the C5 balance check, CNY 1.00 per-order ceiling, CNY
10.00 daily budget, durable per-item reservation, unique `outTradeNo`, unresolved-order
reconciliation, and no automatic resubmission after ambiguity. Ledger rows use
`purchase_kind=MANUAL_TEST`. It must not change the behavior of `monitor`, `once`, dry-run,
scans, due checks, or automatic C5 purchasing.

## Development phases

Go V1 is frozen at tag `v1.0.0`. Go Core, Collector, SQLite, Scan Cycle, Detail Market Fetch,
Tier Scheduler, the long-running runtime, Notification Phase 1, and the QQ official sender are
complete.

V2.1 and V2.2 are complete. V2.3 Phase 1 C5GAME purchase safety and durability are the current
extension. Historical storage and later V2 ideas have not started.

Do not add another delivery provider, multiple-channel routing, goroutine workers, queues, web
UI, Docker, or CI unless the user explicitly starts that phase. The production sender uses only
the QQ HTTP Access Token and active C2C message APIs; do not add the binding probe's WebSocket
Gateway code to the production program.

Production QQ credentials come only from `QQ_BOT_APP_ID`, `QQ_BOT_APP_SECRET`, and
`QQ_BOT_USER_OPENID`. The AppSecret must never be committed or logged.

Run `gofmt`, `go test ./...`, and `go vet ./...` after relevant changes.
