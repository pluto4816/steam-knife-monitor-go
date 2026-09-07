# AGENTS.md

## Source of truth

`GO_V1_SPEC.md` defines the baseline business rules. Explicitly started provisions in
`GO_V2_SPEC.md` override earlier behavior only within their stated scope; all other baseline
rules remain in force. For example, V2.2 targeted scans replace the earlier global exchange
scan. Historical phase descriptions do not undo explicitly documented later extensions.

When first working in this repository, understand the baseline and current scope below. Before
changing code or tests, read the relevant specification sections and their dependent rules using
the index below. Unchanged material already read in the same session need not be reread. For payment
changes, review the selected platform's complete eligibility, budget, durability, and
reconciliation rules. Referenced requirements remain mandatory even when not repeated here.

If a material specification conflict remains unresolved, clarify the affected requirement;
continue independent authorized work. Never resolve a payment or safety conflict by silently
weakening a control.

The sibling Python repository is frozen reference material only. It may be read for verified
requirements, API findings, and test cases, but must not be modified unless the user explicitly
asks.

## Current scope and autonomous work

Go V1 is frozen at tag `v1.0.0`; V2.1 and V2.2 are complete. Purchase development/discussion is paused. The active work is the explicitly started four-page
selling assistant, documented in [V2.4](GO_V2_SPEC.md#v24-selling-assistant).
Existing ECO manual and automatic purchasing remain implemented. Preserve existing C5 implementation; C5 live testing
remains suspended. ECO and C5 purchasing remain mutually exclusive. ECO manual purchasing
still targets the fixed P250 test item; general manual item selection has not started.

The final product target includes automatic and manual purchasing for BUFF, 悠悠有品, C5,
IGXE, and ECO. That target does not start new purchasing work on the other four platforms,
authorize new endpoints, or authorize a multi-platform framework. Historical market sampling and other
deferred V2 work have not started; the explicitly started manual holdings ledger is in scope. Use this section as the current-scope summary and the linked
specification sections for behavior and acceptance criteria.

Within an authorized task, complete necessary cross-package fixes, relevant tests, and
documentation updates without reopening completed phases. A direct user request to implement
a phase supplies its development-scope authorization; do not require a separate ceremonial
confirmation. Resolve routine implementation choices autonomously and ask only when a material
ambiguity or missing authorization prevents the dependent work. Existing session authorization
continues to apply within its scope.

Development authorization alone does not authorize live payments, external messages, or production
operations. Check the user's task and existing session authorization before those actions; do not
request approval again when they are already covered. Credentials or example commands alone are
not authorization. Sandbox and permission approvals still apply.

## Specification index

- Core classification, hysteresis, timing, trends, and notifications:
  [V1 specification](GO_V1_SPEC.md).
- Modes and preview isolation: [V2.1](GO_V2_SPEC.md#v21-goal), with scanning superseded by
  [V2.2 targeted market scan](GO_V2_SPEC.md#targeted-market-scan).
- Candidate refresh and catalog validation:
  [manual candidate update](GO_V2_SPEC.md#manual-candidate-update) and
  [candidate catalog](GO_V2_SPEC.md#candidate-catalog).
- Checkpoints, complete-catalog validation, restart receipts, and scan phase boundaries:
  [targeted market scan](GO_V2_SPEC.md#targeted-market-scan).
- Purchase scope and channel semantics:
  [purchase product model](GO_V2_SPEC.md#purchase-product-model-five-platforms-two-purchase-channels).
- C5 automatic confirmation, limits, and reconciliation: [V2.3 Phase 1](GO_V2_SPEC.md#v23-phase-1-goal)
  through its acceptance criteria; manual exception:
  [manual specified-item purchase test](GO_V2_SPEC.md#manual-specified-item-purchase-test).
- ECO credentials, endpoints, manual limits, and reconciliation:
  [ECO manual test](GO_V2_SPEC.md#eco-manual-single-item-purchase-test-started-2026-09-06);
  automatic quote ordering, migration, and shared accounting:
  [ECO automatic phase](GO_V2_SPEC.md#eco-automatic-purchase-phase-started-2026-09-06).

- Manual holdings, selling targets, actual sale receipts, and mobile web access:
  [V2.4 selling assistant](GO_V2_SPEC.md#v24-selling-assistant).

## Product constraints

The production target remains one server, one process, one user, one notification channel,
SQLite, and one Go executable. QQ official bot C2C messaging is the only notification channel.

Prefer standard-library functions and concrete types. Add an interface only when there are two
current implementations or when testing an external side effect clearly requires one.

Do not overdesign. Implement only the authorized scope under the applicable specification, and do
not introduce microservices, queues, worker pools, leases, distributed coordination, an ORM, a
web framework, dependency-injection framework, dynamic plugin system, event bus, or speculative
abstractions. The V2.2 candidate-source collector is an isolated built-in manual mode, not a
general plugin framework.

Ratio business logic must use `core.RatioBP`; never compare ratios through `float64`. Prices
should remain exact strings until the real API representation is confirmed.

## Data access

Use only the smis.club endpoints and fields already verified in the frozen Python findings.
Candidate updates may also read the ByMykel CSGO-API flat English skin-definition snapshot and
fields documented in V2.2. Purchase modes may use only the C5GAME, ECO, and Steam
`/market/priceoverview/` endpoints permitted for that mode by the indexed specifications.
Treat the definition snapshot as mutable input derived from CS2 game definitions, not as
Steam market-state evidence.

Favor low-frequency, sequential, batched, and cached access. Do not bypass access controls,
CAPTCHA, Cloudflare, rate limits, or other site restrictions. Stop or back off on empty
responses, 401, 403, 429, challenges, and similar restrictions.

The ByMykel snapshot collector runs only in explicit `update-candidates` mode. Normal modes read
the persisted catalog, make no ByMykel requests, and make no Steam Community Market requests
during targeted full scans or due checks. They must not paginate the global smis.club exchange
endpoint. They fetch only verified smis.club single-item details for catalog IDs. The only Steam
Community Market exceptions are automatic C5 and ECO purchase confirmation under their indexed
platform specifications. `/market/priceoverview/` must never be used for candidate collection,
global scanning, ordinary tier persistence, or due-check processing.

Absence is not market-state evidence. Only successfully parsed and validated MarketData may
change a tier. A failed candidate update must preserve the prior valid catalog.

Scans cover all items in the current persisted catalog; no historical candidate count is a
fixed requirement. Preserve exact catalog fingerprint, count, identity, and checkpoint checks.
Incomplete scans must not reach due checks, notifications, or purchases. Only `monitor` may
use the specified recent-completion restart shortcut; `once` and `preview` may not. Follow the
indexed targeted-scan specification for checkpoint persistence and 24-hour scheduling.

## Payment safety

Follow the indexed platform specifications for the exact decision order and manual exceptions.
Automatic purchases require complete scan evidence (or a validated monitor restart receipt),
successful due checks, current A-tier candidates, exact identity, and the selected platform as
the lowest positive SMIS price at or below CNY 1.00. Exclude handled or otherwise ineligible
items before Steam access. ECO additionally validates its official quote before Steam access.
Steam confirmation must yield a positive exact CNY price and recomputed base A without
hysteresis. Failure or restriction prevents purchase. Confirmation prices never replace ordinary
SMIS tier state; SMIS transaction volume is screening evidence, not a payment amount.

Preserve the CNY 1.00 per-order ceiling, CNY 10.00 Asia/Shanghai daily limit, balance checks,
durable item and budget reservation before submission, unique merchant numbers, duplicate
prevention, and original-order reconciliation. Ambiguous orders must never be automatically
resubmitted or have their reservations cleared to enable another attempt. Preserve ECO/C5
shared budget and duplicate checks, existing ledger rows during migrations, and automatic
stop-after-first-success behavior. Order creation is not proof of Steam receipt.

C5 and ECO manual modes retain their specified eligibility exceptions and durable safety
controls; neither calls SMIS or Steam. Automatic dry-run follows its platform's confirmation
rules but makes no balance query, purchase-ledger mutation, or purchase submission. C5 dry-run
makes no C5 request; ECO dry-run requires credentials for its signed quote. The surrounding
`monitor`/`once` still has ordinary SQLite and QQ effects.

Use only the documented platform endpoints and credential handling from the indexed sections.
Never commit or log credentials or other sensitive payment material. Do not add recharge,
withdrawal, Steam login/Guard actions, or automatic trade acceptance.

## Development and verification boundaries

The V2.4 mobile web UI and its HTTP request handling are explicitly started; use standard-library
net/http and html/template within the same executable. Do not add another delivery provider,
multiple-channel routing, background worker pools, queues, unrelated web UI, Docker, or CI unless
the user explicitly starts that phase. The production sender uses only
the QQ HTTP Access Token and active C2C message APIs; do not add the binding probe's WebSocket
Gateway code to the production program.

Production QQ credentials come only from `QQ_BOT_APP_ID`, `QQ_BOT_APP_SECRET`, and
`QQ_BOT_USER_OPENID`. The AppSecret must never be committed or logged.

Run `gofmt`, `go test ./...`, and `go vet ./...` after relevant changes.
