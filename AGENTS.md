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
abstractions. The V2.2 Steam collector is an isolated built-in manual mode, not a general plugin
framework.

Ratio business logic must use `core.RatioBP`; never compare ratios through `float64`. Prices
should remain exact strings until the real API representation is confirmed.

## Data access

Use only the smis.club endpoints and fields already verified in the frozen Python findings.
V2.2 may also use only the verified Steam Community Market filtered search fields documented in
`GO_V2_SPEC.md`. Treat that Steam-hosted search as changeable web behavior, not a documented
public Steamworks Web API.

Favor low-frequency, sequential, batched, and cached access. Do not bypass access controls,
CAPTCHA, Cloudflare, rate limits, or other site restrictions. Stop or back off on empty
responses, 401, 403, 429, challenges, and similar restrictions.

The Steam collector runs only in explicit `update-candidates` mode. Normal modes read the
persisted catalog, make no Steam requests, and must not paginate the global smis.club exchange
endpoint. They fetch only verified smis.club single-item details for catalog IDs.

Absence is not market-state evidence. Only successfully parsed and validated MarketData may
change a tier. A failed candidate update must preserve the prior valid catalog.

## Development phases

Go V1 is frozen at tag `v1.0.0`. Go Core, Collector, SQLite, Scan Cycle, Detail Market Fetch,
Tier Scheduler, the long-running runtime, Notification Phase 1, and the QQ official sender are
complete.

V2.1 bounded execution is complete. V2.2 manual candidate refresh, candidate-catalog
persistence, and targeted smis.club detail scanning are the current extension. Historical
storage and later V2 ideas have not started.

Do not add another delivery provider, multiple-channel routing, goroutine workers, queues, web
UI, Docker, or CI unless the user explicitly starts that phase. The production sender uses only
the QQ HTTP Access Token and active C2C message APIs; do not add the binding probe's WebSocket
Gateway code to the production program.

Production QQ credentials come only from `QQ_BOT_APP_ID`, `QQ_BOT_APP_SECRET`, and
`QQ_BOT_USER_OPENID`. The AppSecret must never be committed or logged.

Run `gofmt`, `go test ./...`, and `go vet ./...` after relevant changes.
