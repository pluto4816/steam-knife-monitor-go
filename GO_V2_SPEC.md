# Steam Knife Monitor — Go V2 Specification

## Baseline

Go V1 is frozen at commit `fa5157efb937c46fce4246e2b371c4d67207eb22` and local
annotated tag `v1.0.0`. `GO_V1_SPEC.md` remains the source of truth for all existing
classification, scheduling, persistence, data-access, and QQ delivery behavior.

V2 extends that verified behavior. It must not silently change V1 business rules.

## Design discipline

Do not overdesign V2.

- Work one explicitly selected phase at a time.
- Prefer the standard library, concrete types, and the existing packages.
- Reuse the V1 scan, processing, SQLite, and notification paths instead of copying them.
- Add only the smallest behavior required by the current phase.
- Do not introduce speculative abstractions, provider routing, worker pools, queues,
  microservices, an ORM, a web framework, a plugin system, or distributed coordination.
- Continue to use `core.RatioBP` for ratio business logic and exact strings for prices.
- Continue to stop or back off on access restrictions. Preview is not permission to bypass
  smis.club controls or increase request frequency.

## V2.1 goal

Add bounded execution modes so the program can be used manually without starting the
long-running monitor.

The executable has three modes:

- `monitor`: the existing V1 long-running behavior and the default mode.
- `once`: perform one bounded production cycle and exit.
- `preview`: perform one isolated preview cycle and exit without production mutations or QQ
  delivery.

## One-time production mode

`once` uses the configured SQLite database and the production QQ sender. It:

1. performs one full candidate and exchange scan;
2. waits the configured detail-request delay;
3. processes the item states that are due at that time;
4. sends the pending durable alerts once; and
5. prints a summary and exits.

It does not start the polling loop, schedule an internal retry, or wait for the next interval.
QQ credentials remain mandatory. State transitions, pending alert creation, SENT marking, and
at-least-once delivery semantics are the same as V1.

`once` optionally accepts `-notify-empty`. After a complete successful scan, it counts only
items in the current persisted candidate catalog. If none is tier A, it sends the exact text
`没有合适物品` through the production QQ sender after pending durable alerts. This completion
message is not persisted as a market alert and has no internal retry. The flag is rejected in
`monitor`, `preview`, and `update-candidates`.

An error ends the bounded run and is returned to the caller. Access restrictions must stop the
run immediately.

## Preview mode

`preview` performs the same bounded scan and processing path against a fresh in-memory SQLite
database. It:

- reads real smis.club data using the V1 endpoints and conservative delays;
- does not open or modify the configured production database;
- does not require QQ credentials, construct a QQ sender, call QQ, or mark an alert SENT;
- reports scan counts, tier counts, due-check counts, and the pending alert messages that would
  have been sent; and
- exits after that single bounded cycle.

Because its database starts empty, preview describes a fresh-state run. It does not reproduce
hysteresis or improvement-alert history from a production database. That limitation must remain
visible in the documentation and output rather than being hidden behind a complex database-copy
mechanism.

Preview is side-effect-free with respect to production state and QQ delivery, but it still makes
normal read requests to smis.club.

## V2.1 compatibility

- Running the executable without a mode keeps the V1 `monitor` behavior.
- `monitor` and `once` use the `-db` path; `preview` uses isolated in-memory SQLite.
- Existing V1 timing defaults remain unchanged.
- Absence, partial scans, and invalid market data remain unable to change a tier.
- Only QQ official bot C2C delivery exists; V2.1 adds no notification provider.

## V2.1 acceptance criteria

- Mode parsing rejects unknown modes.
- `once` executes one bounded cycle, can persist state and send pending alerts, and then returns.
- `preview` completes without QQ credentials and without modifying the production database.
- Preview output clearly identifies fresh-state semantics and displays would-send alert messages.
- Cancellation and access restrictions stop both bounded modes.
- Existing long-running monitor tests remain valid.
- `gofmt`, `go test ./...`, and `go vet ./...` pass.

## V2.2 goal

Replace the expensive global exchange crawl with a manually refreshed candidate catalog and
targeted smis.club detail reads.

Do not overdesign this as a dynamic plugin system. The candidate-source collector is an isolated
built-in execution mode in the existing executable. It runs only when explicitly selected and
shares data with the normal modes through one versioned JSON catalog.

## Manual candidate update

`update-candidates` is a bounded manual mode. It does not open the production SQLite database,
construct a QQ sender, send QQ messages, or start the monitor. It:

1. downloads the ByMykel CSGO-API English `skins_not_grouped.json` game-definition snapshot and
   filters exact structured fields for `rarity.id == rarity_rare_weapon`,
   `wear.name == Field-Tested`, `stattrak == false`, and `souvenir == false`;
2. reads the already verified smis.club candidate search using the equivalent V1 filters;
3. intersects snapshot `market_hash_name` values with smis.club numeric item IDs through the
   verified single-item detail endpoint;
4. reuses still-valid mappings from the previous catalog so later refreshes normally need
   detail requests only for new or changed items; and
5. atomically replaces the catalog only after every required page and detail response was
   successfully parsed and validated.

The source URL is
`https://raw.githubusercontent.com/ByMykel/CSGO-API/main/public/api/en/skins_not_grouped.json`.
The upstream CSGO-API project is MIT-licensed and generates its structured data from CS2 game
files. Its `main` snapshot is mutable, so each successful download is bounded to 64 MiB, fully
decoded and validated, and identified in the completion summary by the SHA-256 of the exact
response bytes.

The definition snapshot and smis.club use different identifiers. A definition without a
matching smis.club ID is reported by `market_hash_name` and omitted because it cannot yet be
monitored. It is not a reason to invent an ID. The snapshot describes items defined by the game;
it does not prove current Steam Community Market availability. Current ratio, volume, and price
evidence continues to come only from a successfully validated smis.club single-item detail.

The update performs one snapshot request followed by conservative sequential smis.club requests,
with no internal retry. It immediately stops on an oversized or empty response, 401, 403, 429,
challenge HTML, malformed data, inconsistent filter fields, duplicate candidate names, or
similar restrictions. It never calls Steam Community Market. An incomplete update never
replaces a previously valid catalog, and neither source absence nor a definition-list change is
market-state evidence.

## Candidate catalog

The default catalog path is `./steam-candidates.json` and can be changed with `-candidates`.
The catalog contains only:

- a schema version and generation time;
- the fixed V1 candidate criteria; and
- sorted smis.club item IDs with `market_hash_name` and Chinese display name.

It contains no credentials. `monitor`, `once`, and `preview` load this catalog at startup and
fail clearly when it is missing, empty, or invalid. They never download the ByMykel snapshot,
call Steam Community Market, or run the smis.club candidate search implicitly.

## Targeted market scan

The production scan no longer paginates `/api/commodity/exchange`. It iterates the persisted
candidate IDs and calls the already verified `/api/commodity/{id}` detail endpoint sequentially
with the existing conservative request delay. Each valid detail supplies the ratio, exact price
text, and `steamTransactionQuantity` used by the unchanged V1 classifier.

The same validated detail also selects the lowest strictly positive sell price among BUFF,
悠悠有品, C5, IGXE, and ECO for alert display. Zero prices mean unavailable and are ignored.
Prices are compared exactly without floating point and the selected JSON number text is preserved.
Exact ties use that listed platform order. This display selection does not change V1
classification. New persisted market alerts use:

```text
名称：{name_cn}
折扣：{ratio}
steam成交量：{steamTransactionQuantity}
steam价格：{steamSellPrice}
平台：{lowest platform}
平台价格：{exact lowest platform price}
```

An absent, invalid, restricted, or incomplete detail response cannot change that item's stored
state. The bounded run stops on the first error and does not send notifications from an
incomplete run. HTTP 2xx responses with an empty or whitespace-only body are treated as access
restriction or equivalent upstream soft failure and stop immediately without an internal retry.

The targeted scan stores one SQLite checkpoint after each successfully validated item has been
persisted. The checkpoint contains the SHA-256 fingerprint of the exact sorted catalog, the
completed prefix length, and the last completed item ID. A later invocation with the same
catalog resumes at the first unfinished item. A different catalog invalidates the checkpoint and
starts from its first item. Completing the full catalog clears the checkpoint before due checks
or notification delivery. State persistence precedes checkpoint persistence, so a crash between
the two may repeat one safe item but can never skip an unpersisted item.

Resumed items count only as a completed prefix; they are not fetched again and are reported
separately in scan summaries. Even with a checkpoint, no due-check, tier summary, pending-alert
delivery, or `-notify-empty` completion message may run until the entire catalog has completed.

## V2.2 compatibility and acceptance criteria

- V1 classification, scheduling, hysteresis, notification, SQLite, and QQ semantics do not
  change.
- The candidate update mode needs neither SQLite nor QQ credentials.
- The normal modes make no ByMykel, Steam Community Market, or exchange requests.
- Preview continues to use fresh in-memory SQLite while reading the same external candidate
  catalog as the production modes.
- Candidate replacement is atomic and a failed update preserves the prior catalog.
- Interrupted targeted scans resume from a checkpoint only for the exact same catalog; catalog
  changes reset progress, successful completion clears it, and incomplete scans send nothing.
- Alert messages report the lowest positive BUFF, 悠悠有品, C5, IGXE, or ECO sell price using
  exact comparison and the source price text.
- Repeated candidate updates reuse valid mappings and fetch details only where mapping is
  missing or no longer matches the definition snapshot.
- Successful candidate updates report the definition snapshot SHA-256 and every omitted
  definition-only `market_hash_name`.
- `-notify-empty` is accepted only by `once` and sends only after a complete successful run
  whose current catalog has no tier-A item.
- ByMykel/GitHub and smis.club access restrictions stop the active bounded operation immediately.
- `gofmt`, `go test ./...`, and `go vet ./...` pass.

## Deferred V2 phases

Historical samples and decision explanations, operational tooling, and evidence-based rule
changes remain possible later phases. They are not part of V2.1 or V2.2 and must not be
implemented as part of the candidate-catalog work.
