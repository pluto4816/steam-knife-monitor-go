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
it does not prove current Steam Community Market availability. In the V2.2 baseline, current
ratio, volume, and price evidence continues to come only from a successfully validated
smis.club single-item detail; the explicitly enabled V2.3 exception is defined below.

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
or run the smis.club candidate search implicitly. Steam price requests occur only when C5
purchasing is explicitly enabled, under the V2.3 rules below.

## Targeted market scan

The production scan no longer paginates `/api/commodity/exchange`. It iterates the persisted
candidate IDs and calls the already verified `/api/commodity/{id}` detail endpoint sequentially
with the existing conservative request delay. Each valid detail supplies the preliminary ratio,
exact price text, and `steamTransactionQuantity` used by the V2.2 baseline classifier. The
explicitly enabled V2.3 price-verification rule is defined below.

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

After a complete targeted scan clears its checkpoint, it persists a completion receipt with the
exact catalog fingerprint, candidate count, and UTC completion time. On `monitor` restart only,
the initial full scan is skipped when the receipt is less than 24 hours old, its fingerprint and
count match the exact current catalog, every catalog item ID, `market_hash_name`, and display
name has exactly one persisted state, and no checkpoint exists. The next full scan remains due
24 hours after the persisted completion rather than 24 hours after restart. The resumed monitor
runs due checks before its first purchase phase. A missing, future, expired, or mismatched
receipt, any incomplete or extra state, or any checkpoint requires the normal targeted full
scan. `once` and `preview` never use this restart shortcut.

Resumed items count only as a completed prefix; they are not fetched again and are reported
separately in scan summaries. Even with a checkpoint, no due-check, tier summary, pending-alert
delivery, or `-notify-empty` completion message may run until the entire catalog has completed.

## V2.2 compatibility and acceptance criteria

- V1 classification, scheduling, hysteresis, notification, SQLite, and QQ semantics do not
  change.
- The candidate update mode needs neither SQLite nor QQ credentials.
- Targeted scans and due checks make no ByMykel, Steam Community Market, or exchange requests.
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

## V2.3 Phase 1 goal

Add an explicitly enabled C5GAME purchase phase while preserving behavior when it is off and
preserving the V2.2 scan-completeness boundary. This phase supports only C5GAME's documented
OpenAPI and only the current single user. Other platforms remain out of scope.

The C5 integration is retained but live testing is suspended because the user reports that
their C5 API account cannot obtain purchase permission. The explicitly authorized ECO manual
test and automatic ECO phase below are the current platform scope.

The purchase mode is `off`, `dry-run`, or `live`, selected with `-c5-purchase`. It defaults to
`off`. `monitor` and `once` accept all three values. `preview` and `update-candidates` require
`off`. Live mode reads `C5_APP_KEY` and `C5_TRADE_URL` from the environment; neither value may be
committed or logged.

Phase 1 has fixed safety limits:

- at most one item is bought for a given catalog item ID;
- the submitted price ceiling is at most CNY 1.00 per order;
- committed plus unresolved purchases are at most CNY 10.00 per Asia/Shanghai calendar day;
- no recharge, withdrawal, bargain, buy order, batch order, Steam login, Steam Guard action, or
  automatic trade-offer acceptance is performed.

## Steam price verification

Targeted full scans and due checks always use only validated smis.club detail data. They never call
Steam Community Market, even when `-c5-purchase dry-run` or `live` is enabled. Their persisted
ratio, Steam price, tier state, checkpoint, and ordinary QQ alerts therefore remain based on SMIS.

The purchase phase starts from persisted current A-tier candidates only after the full scan and
due-check phases succeed. It refreshes the smis.club single-item detail and, before any Steam
request, requires the exact catalog identity, C5 as the lowest strictly positive external
platform, and a positive exact C5 price no greater than CNY 1.00. Candidates that fail any of
those checks do not call Steam.

Only a candidate that passes that purchase prefilter queries Steam Community Market
`/market/priceoverview/` with app ID `730`, currency `23`, and the exact catalog
`market_hash_name`. The endpoint is hosted by Steam Community Market but is not a documented
Steamworks Web API. The implementation accepts only a successful, positive CNY `lowest_price`,
preserves it as an exact decimal string, and recomputes `core.RatioBP` exactly from it and the
refreshed smis.club C5 price. The recomputed evidence must satisfy the base A rule without
hysteresis before any balance query or purchase submission. This Steam value is a
payment-confirmation input; it does not replace ordinary SMIS state or alert data.

`steamTransactionQuantity` continues to come from smis.club and remains only a classification
screening input; it is not a payment amount or direct purchase limit. Steam requests are
sequential and have no internal retry. An empty response, 401, 403, 429, challenge HTML,
malformed response, unsuccessful response, missing price, or invalid currency prevents purchase
and ends that purchase attempt without submitting to C5. When C5 purchasing is `off`, no Steam
Community Market request is made.

## C5 purchase eligibility and confirmation

A purchase phase may run only after the active targeted full scan has completed and cleared its
checkpoint, or after `monitor` restart has verified the recent durable full-scan receipt and
exact complete catalog state under the restart rule above. It runs after a successful due-check
phase and before ordinary QQ alert delivery. A checkpoint, incomplete state, expired or
mismatched receipt, incomplete full scan, or failed due-check performs no purchase work before
the normal full scan succeeds.

Each persisted current A-tier catalog item not already bought or unresolved is re-fetched from
the verified smis.club single-item detail endpoint immediately before a decision. The checks run
in this fixed order:

1. require the exact catalog item ID and `market_hash_name`;
2. require C5 as the lowest strictly positive external sell platform;
3. require a positive exact smis.club C5 price no greater than CNY 1.00;
4. request one positive exact Steam `lowest_price`;
5. recompute `core.RatioBP` from the Steam price and C5 price; and
6. require the recomputed evidence to satisfy base A without hysteresis.

Dry-run mode stops after this validation and reports what it would submit. It requires no C5
credential and never calls C5GAME or creates a purchase record.

Every current persisted A-tier candidate excluded in dry-run must produce a log entry with a
stable reason code and the relevant non-secret persisted or refreshed values. Dry-run phase and
one-shot summaries must report the number of excluded candidates.

Live mode queries the documented C5GAME account-balance endpoint before submitting. The exact
refreshed smis C5 price, not the CNY 1.00 absolute limit, becomes `maxPrice` on C5GAME's
documented quick-buy request. The request uses `marketHashName`, app ID 730, the configured trade
URL, and a durable unique `outTradeNo`; it leaves optional delivery and low-price selectors
unset so C5GAME applies its documented default lowest-price selection. A successful response
must contain a positive `actualPay` no greater than the submitted ceiling and a positive order
ID.

## C5 purchase durability

Before the first quick-buy request, SQLite atomically reserves the item, the daily budget, the
quoted amount, and `outTradeNo` with status `SUBMITTING`. `SUBMITTING` and `UNCERTAIN` amounts
remain reserved in the daily budget. A successfully created order becomes `PLACED`. An
application-level failure must currently become `UNCERTAIN`: the published API does not define
which error codes prove that no order was created. `REJECTED` is reserved for a future explicitly
verified definitive-rejection code, and no second attempt is made for that item on the same
calendar day.

An HTTP, transport, cancellation, malformed-response, or local persistence ambiguity after a
submission starts must never cause an automatic resubmission. The purchase becomes `UNCERTAIN`.
Before any new live purchase, unresolved rows are queried through C5GAME's documented order
detail endpoint using the original `outTradeNo`. A found order becomes `PLACED`; any unresolved
or failed reconciliation stops new purchase submission for that phase.

C5GAME requests are sequential, use no internal retry, and treat 401, 403, 429, empty responses,
and challenge-like HTML as access restrictions. The app key is placed only in the documented
query parameter and must be redacted from all returned errors.

## Manual specified-item purchase test

The explicit one-shot `purchase-test` command is an authorized exception to the automatic
purchase eligibility flow. It must:

1. require `-c5-purchase live` and one positive catalog `item_id`;
2. resolve the exact `market_hash_name` and Chinese name from the existing persisted catalog;
3. make no SMIS or Steam request and require neither SMIS identity/tier state nor C5-lowest;
4. submit exactly one quick-buy with `maxPrice=1.00`; and
5. exit after success or the first failure without automatic quick-buy retry.

The only market-price eligibility condition is the server-enforced CNY 1.00 ceiling. The
ordinary balance, CNY 10.00 Asia/Shanghai daily budget, durable per-item reservation, unique
trade number, and unresolved-order reconciliation controls remain mandatory. A successful
response must report a positive `actualPay` no greater than CNY 1.00. Its ledger row is marked
`MANUAL_TEST`; existing rows migrate to `AUTO`. This exception changes no behavior in
`monitor`, `once`, dry-run, targeted scans, due checks, or automatic purchase confirmation.

## V2.3 Phase 1 acceptance criteria

- Existing operation is unchanged when `-c5-purchase off` is omitted.
- Targeted scans and due checks never call Steam; purchase confirmation calls it only after the
  C5-lowest and CNY 1.00 prefilter.
- Dry-run performs real smis and Steam confirmation but no C5 request or SQLite purchase mutation.
- Dry-run reports each exclusion reason and its total without logging credentials.
- Live mode cannot start without both C5 environment values.
- Partial scans and failed due checks cannot reach the purchase phase.
- A monitor restart skips its initial full scan only with a matching recent durable completion,
  exact complete catalog state, and no checkpoint; its first purchase still follows due checks.
- Automatic purchases enforce Base-A, C5-lowest, exact identity, exact price, balance, per-order,
  per-item, and daily limits before quick-buy.
- `purchase-test` bypasses only SMIS/Steam/tier/platform comparisons, submits the selected
  catalog identity once with `maxPrice=1.00`, and records `MANUAL_TEST`.
- An ambiguous submission is never retried with a new order number.
- Restart reconciliation uses the original `outTradeNo` before any new purchase.
- `gofmt`, `go test ./...`, and `go vet ./...` pass.

## ECO manual single-item purchase test (started 2026-09-06)

The user has obtained ECO API and purchase permissions and authorized one live attempt for
`P250 | Re.built (Field-Tested)` / `P250 | 重构 (久经沙场)`.

`-mode eco-purchase-test` is an isolated mode of the same executable. It requires C5 purchasing
off, does not require the local candidate catalog, and performs no SMIS, Steam, C5, QQ,
candidate collection, tier processing, or ordinary monitor work. The target identity is fixed;
this phase adds no general platform routing or automatic ECO purchase phase.

Credentials are `ECO_PARTNER_ID`, `ECO_PRIVATE_KEY_FILE` (matching the public key registered
with ECO), and `ECO_TRADE_URL`. Identity, private key, trade-link token, signatures, raw request
bodies, and raw upstream error messages must never be committed or logged. RSA keys of at least
2048 bits in PKCS#1/PKCS#8 PEM or base64 DER are supported. Production uses HTTPS on the fixed
official host, refuses redirects, and makes sequential requests with a one-second gap and no
internal retry.

Only these official ECO endpoints are allowed in this phase:

- `POST https://openapi.ecosteam.cn/Api/Merchant/GetTotalMoney`
  ([wallet](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/api-220956645));
- `POST https://openapi.ecosteam.cn/Api/open/buy/BuyByHashName`
  ([purchase](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/api-220956656));
- `POST https://openapi.ecosteam.cn/Api/open/order/OrderDetailsInfo`
  ([order](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/api-220956661)).

The [official signing example](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/doc-5242863)
sorts top-level names case-insensitively and signs the unescaped `key=value&...` string with
SHA256withRSA (PKCS#1 v1.5), base64-encoded. Requests include PartnerId and a Unix-seconds
Timestamp. [Common responses](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/doc-5242864)
use ResultCode 0 for success; both string and integer representations are documented. Missing,
null, malformed, restricted, nonzero, empty or oversized responses stop the attempt.

The purchase sends GameId `730`, the exact HashName, numeric Price `1` (CNY 1.00) and MaxCount `1`,
the user's TradeLink and a durable unique MerchantNo. Optional IsFirst (express delivery),
StyleName and service-provider fields are omitted. Price is documented as the maximum purchase
budget. Monetary JSON tokens are parsed exactly into integer fen without float64 arithmetic.
The integer price token avoids trailing-zero normalization differences in signing.

Before submitting, conservatively require wallet Money minus LockMoney >= CNY 1.00. In SQLite,
reserve the item, full CNY 1.00 and merchant number atomically as SUBMITTING with
purchase_kind=MANUAL_TEST. The Asia/Shanghai daily limit is CNY 10.00, including existing C5
committed/unresolved amounts in that same database. Keep the entire ECO ceiling reserved even
after success; the one-item test permits no second purchase. Use the same persistent database
on every invocation, with the existing one-process/one-user operating constraint and C5 live
work suspended. In-memory and SQLite URI paths are rejected.

A purchase response must contain exactly one order, matching MerchantNo, a positive order
number, a positive PaymentPrice <= CNY 1.00, and no duplicate-order ID list. Persist PLACED
before the final detail query. Every submission ambiguity becomes UNCERTAIN, or remains
SUBMITTING if persistence fails. All existing ECO rows prevent another purchase, including
after cancellation or on a later calendar day.

On subsequent invocations, only query the original MerchantNo and exit. The official detail
schema permits OrderNum or MerchantNo. A missing order never clears a reservation. Validated
detail requires matching MerchantNo and HashName, the same order number when already known,
an explicit order-state code, and positive TotalMoney with TotalMoney plus BuyerFee <= CNY 1.00
(conservative until actual fee representation is verified). Record platform order state
separately; PLACED means order creation, never proof of Steam receipt or completed settlement.
No recharge, withdrawal, Steam login/Guard operation, automatic trade acceptance, callback
server, WebSocket, or QQ receipt is added.

Acceptance: signature and exact request checks; malformed/nonzero/over-ceiling/multi-order
response rejection; no redirects or retries; durable reservation before payment; cancellation
and restart recovery; insufficient balance and daily-budget refusal; repeated invocation only
queries; isolated command routing; `gofmt`, `go test ./...`, and `go vet ./...`.

## ECO automatic purchase phase (started 2026-09-06)

The manual test successfully placed a CNY 0.45 order and the user confirmed receipt. The user
then explicitly authorized integrating automatic ECO buying with the existing limits and
duplicate-prevention controls. This section extends the manual-only boundary above. C5 live
work remains suspended; do not introduce a platform framework.

`-eco-purchase off|dry-run|live` defaults to off. Only `monitor` and `once` accept dry-run/live.
ECO and C5 purchasing cannot be enabled together. `preview`, `update-candidates`, C5
`purchase-test` and `eco-purchase-test` do not accept automatic ECO purchasing. Automatic ECO
uses the existing durable database, catalog, scheduler and QQ sender; it requires a durable
file path. Off makes no ECO requests and performs no ECO schema migration.

Automatic ECO uses the three endpoints above plus one verified official price endpoint:
`POST https://openapi.ecosteam.cn/Api/Market/BatchSearchSellingPrice`
([documentation](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/api-220956651)).
Send GameID `730` and a one-element HashName array for the exact candidate. Require exactly one
matching response, a valid integer SellCount, and positive exact MinPrice when stock exists.
Zero stock excludes the item. Missing, malformed or restricted quotes stop the phase. This
endpoint is a purchase-phase input only; no ECO catalog crawl or listing pagination is added.
The array is serialized as compact JSON for signing, following the official example. A real
read-only query verified this endpoint/signature/response and returned CNY 0.47 for the test
P250 during implementation; this quote is not a future purchase authorization or cached price.

The fixed decision order is:

1. Complete the targeted scan (or verify the durable recent monitor restart receipt) and
   successfully run due checks using SMIS only, as before.
2. In live mode reconcile all ECO SUBMITTING/UNCERTAIN rows and PLACED rows whose detail has
   never been verified, using each original MerchantNo, identity and reserved ceiling. Any
   unresolved/not-found/error blocks new purchases. A reconciliation cycle submits nothing.
3. Select persisted current A-tier catalog identities. Exclude handled items, unresolved
   orders and exhausted budgets before market requests. Both numeric ID and exact hash name
   prevent ECO repeats; previous manual purchases remain handled by hash name. Existing C5
   purchases of the same identity and all unresolved C5 submissions also block purchasing.
4. Refresh the exact SMIS item. Require matching identity, ECO as the lowest positive platform
   under the existing tie order, and an exact positive ECO price <= CNY 1.00.
5. Query ECO's official price. Require a positive quote no greater than the SMIS ceiling.
   An increase or no stock excludes the candidate before any Steam request. Check the daily
   budget against this quote before Steam access.
6. Query the existing Steam Community Market CNY lowest-price source once. Recompute RatioBP
   using the official ECO quote and Steam price; require base A without hysteresis and use
   only SMIS transaction volume. Do not persist these confirmation prices into tier state.
7. Dry-run logs its decision and stops here. It requires ECO credentials for signed quote
   reads, but never queries balance, creates/migrates/mutates ECO ledger data or submits.
   The surrounding `once`/`monitor` still has its ordinary SQLite and QQ effects.
8. Live checks ECO available balance, then atomically reserves exactly the confirmed quote,
   item, original merchant number, and AUTO evidence before submitting one BuyByHashName with
   MaxCount=1 and that exact numeric price ceiling. The ceiling can never exceed CNY 1.00.
9. Validate the result, persist PLACED and query the original order once. Until valid matching
   detail is saved, no later new purchase is allowed. Send the QQ ECO order-created receipt
   after successful verification, preserving the existing stop-after-first-success runtime
   behavior. This is not an assertion of Steam receipt. No automatic offer acceptance is added.

The Asia/Shanghai daily cap stays CNY 10.00. Conservatively keep all ECO reserved ceilings
counted for their reservation day, even after payment or cancellation, plus existing C5
committed/unresolved amounts in the same database. Determine the reservation day immediately
before payment, not at the start of a long scan. Every ECO identity is reserved at most once
across dates, including failures/cancellation; no automatic resubmission or reservation release.
All requests remain sequential with the configured one-second production gap and no internal
retry. The monitor retains its existing five-minute error backoff.

Migrate the old manual ECO table transactionally, preserving every original row and its ID,
merchant number, order number, reserved/paid amounts and state. Add AUTO as a permitted kind
and item ID, display name and confirmed Steam price as purchase evidence. Keep unique hash
names and add a unique index for positive item IDs. An interrupted migration cannot discard
the successful manual order. Dry-run reads the legacy manual schema without migrating it.

Acceptance includes the full decision order; non-A/price/identity/stock/Steam failure guards;
no payment inputs in scans or due checks; no purchase after incomplete scans or failed due
checks; dry-run without payment writes; old manual order preservation and repeat exclusion;
transactional budget and item checks; submission ambiguity and restart reconciliation; exact
quote ceiling/signature tests; and successful existing regression tests, gofmt and go vet.
