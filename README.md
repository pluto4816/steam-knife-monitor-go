# steam-knife-monitor-go

A small Go Steam/CS2 knife-opportunity monitor for one process, one user, one SQLite database,
and QQ official bot C2C notifications.

Go V1 is frozen at tag `v1.0.0`. V2.1 added bounded one-time and preview execution. V2.2 adds
a manually refreshed candidate catalog and replaces the global smis.club exchange crawl with
targeted detail requests.

## Candidate catalog

The monitor now requires a local candidate catalog. Generate or refresh it manually:

```sh
go run ./cmd/monitor \
  -mode update-candidates \
  -candidates ./steam-candidates.json
```

This bounded command:

- downloads ByMykel CSGO-API's English `skins_not_grouped.json` game-definition snapshot and
  filters exact structured fields to `Normal`, `Field-Tested`, and `Mil-Spec Grade` skins;
- fetches the equivalent filtered candidate IDs from smis.club;
- maps snapshot `market_hash_name` values to smis.club numeric item IDs through verified
  single-item details;
- atomically writes `steam-candidates.json` only after the complete update succeeds; and
- exits without opening SQLite, reading QQ credentials, sending a message, or starting the
  monitor.

The snapshot and smis.club use different IDs, so the initial update may need one sequential smis
detail request per candidate to establish mappings. Later updates reuse mappings that still
match and normally request details only for new or changed items. The snapshot is fetched once;
smis requests are sequential and use conservative delays. There is no internal retry or
access-control bypass. The completion log records the exact snapshot SHA-256 and lists each
definition-only `market_hash_name` omitted because no smis.club ID was mapped.

The generated JSON contains no credentials. It is local runtime data and is ignored by Git. It
can be backed up with the SQLite database or regenerated with the command above.

The source is ByMykel's MIT-licensed CSGO-API project:
`https://github.com/ByMykel/CSGO-API`. Its mutable snapshot is structured data generated from CS2
game files, not proof that an item is currently listed on Steam Community Market. Live ratio,
volume, and price evidence still comes only from validated smis.club details. An oversized,
changed, malformed, empty, 401, 403, 429, or challenge response stops the update without
replacing a valid prior catalog. The update mode no longer calls Steam Community Market.

## Execution modes

All normal modes load `./steam-candidates.json` by default. Use `-candidates PATH` to select a
different catalog. They never download the ByMykel snapshot, call Steam Community Market, or
run the smis.club candidate search implicitly.

### Preview

Preview performs one targeted detail scan against fresh in-memory SQLite, prints tier totals and
the messages that would be sent, and then exits:

```sh
go run ./cmd/monitor \
  -mode preview \
  -candidates ./steam-candidates.json
```

Preview does not require QQ credentials, open or modify the production database, call QQ, or
mark alerts SENT. It still reads real smis.club detail data using the conservative request
delay.

Because preview starts with empty state, it does not reproduce production hysteresis or
improvement-alert history. Its output describes a fresh-state run.

### One-time production scan

One-time mode reads each catalog item through `/api/commodity/{id}`, processes currently due
item states, sends pending QQ alerts once, and exits:

```sh
go run ./cmd/monitor \
  -mode once \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

This mode has real production side effects: it updates the selected database and may send QQ
messages. It requires all three QQ environment variables shown below. It does not paginate the
global smis.club exchange endpoint.

After each successfully validated candidate, the scan saves a SQLite checkpoint tied to the
SHA-256 fingerprint of the exact ordered catalog. If an upstream restriction interrupts the
scan, the next invocation with that same catalog resumes at the first unfinished candidate.
Changing or refreshing the catalog invalidates the old checkpoint and starts from the beginning.
The checkpoint is cleared after a complete full scan. An incomplete scan still performs no due
checks and sends no pending or empty-result notification.

To receive an explicit QQ completion message when the successful scan finds no tier-A item in
the current candidate catalog, add `-notify-empty`:

```sh
go run ./cmd/monitor \
  -mode once \
  -notify-empty \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

The message text is `没有合适物品`. This flag is accepted only by `once`; it does not change
`preview`, `monitor`, or `update-candidates`. The completion message is sent directly after
the successful bounded run and is not stored as a durable market alert. Its QQ send failure is
returned to the caller without an internal retry.

### Long-running monitor

The default mode preserves the V1 long-running scheduler while using the V2.2 targeted scan:

```sh
go run ./cmd/monitor \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

The equivalent explicit command is:

```sh
go run ./cmd/monitor \
  -mode monitor \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

Conservative runtime defaults are a 24-hour full candidate-detail scan, 60-second due poll,
1-second detail-request delay, and 5-minute ordinary-error delay. These are operating defaults,
not upstream official limits.

## QQ credentials

Production `monitor` and `once` modes require:

```bash
export QQ_BOT_APP_ID='your-app-id'
export QQ_BOT_USER_OPENID='your-user-openid'
read -rsp 'QQ AppSecret: ' QQ_BOT_APP_SECRET
printf '\n'
export QQ_BOT_APP_SECRET
```

Never commit or log the AppSecret. The production modes stop at startup if any required QQ
credential is missing. `preview` and `update-candidates` do not read QQ credentials.

The QQ sender uses the HTTP Access Token and active C2C APIs only. Tokens are cached according
to `expires_in` and refreshed within 60 seconds of expiry. Delivery remains at-least-once: a
PENDING alert may be resent after restart if QQ accepted it but SQLite did not record SENT.

## Verification

```sh
go test ./...
go vet ./...
```

See `GO_V1_SPEC.md` for the frozen business rules and `GO_V2_SPEC.md` for the active V2 phase
boundary.
