# AGENTS.md

## Source of truth

Before changing code or tests, read `GO_V1_SPEC.md`. It is the concise source of truth for
the Go V1.

The sibling Python repository is frozen reference material only. It may be read for verified
requirements, API findings, and test cases, but must not be modified unless the user explicitly
asks.

## V1 constraints

The production target is one server, one process, one user, one notification channel, SQLite,
and one Go executable. QQ official bot C2C messaging is the only V1 notification channel.

Prefer standard-library functions and concrete types. Add an interface only when there are two
current implementations or when testing an external side effect clearly requires one.

Do not introduce microservices, queues, worker pools, leases, distributed coordination, an ORM,
a web framework, dependency-injection framework, plugin system, event bus, or speculative
abstractions.

Ratio business logic must use `core.RatioBP`; never compare ratios through `float64`.
Prices should remain exact strings until the real API representation is confirmed.

## Data access

Use only the smis.club endpoints and fields already verified in the frozen Python findings.
Favor low-frequency, batched, cached access. Do not bypass access controls, CAPTCHA, Cloudflare,
rate limits, or other site restrictions. Stop or back off on 401, 403, 429, challenges, and
similar restrictions.

Absence is not market-state evidence. Only successfully parsed and validated MarketData may
change a tier.

## Development phases

Work one phase at a time. Go Core, Collector, SQLite, Scan Cycle, Detail Market Fetch, Tier
Scheduler, the long-running runtime, Notification Phase 1, and the QQ official sender are
complete.

For V1, do not add another delivery provider, multiple-channel routing, goroutine workers,
queues, web UI, Docker, or CI unless the user explicitly starts that work. The production sender
uses only the QQ HTTP Access Token and active C2C message APIs; do not add the binding probe's
WebSocket Gateway code to the production program.

Production QQ credentials come only from `QQ_BOT_APP_ID`, `QQ_BOT_APP_SECRET`, and
`QQ_BOT_USER_OPENID`. The AppSecret must never be committed or logged.

Run `gofmt`, `go test ./...`, and `go vet ./...` after relevant changes.
