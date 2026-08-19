# AGENTS.md

## Source of truth

Before changing code or tests, read `GO_V1_SPEC.md`. It is the concise source of truth for
the Go V1.

The sibling Python repository is frozen reference material only. It may be read for verified
requirements, API findings, and test cases, but must not be modified unless the user explicitly
asks.

## V1 constraints

The production target is one server, one process, one user, one notification channel, SQLite,
and one Go executable.

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

Work one phase at a time. Go Core, Collector, SQLite, Scan Cycle, and Detail Market Fetch are
complete. The current phase is Tier Scheduler.

During the Tier Scheduler phase, do not add notifications, a permanent loop, goroutine workers,
web UI, Docker, or CI.

Run `gofmt`, `go test ./...`, and `go vet ./...` after relevant changes.
