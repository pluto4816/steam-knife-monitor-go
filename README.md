# steam-knife-monitor-go

A clean Go rewrite of the single-process Steam/CS2 knife-opportunity monitor.

Go Core, Collector, SQLite persistence, Scan Cycle, Detail Market Fetch, Tier Scheduler, the
long-running runtime, Notification Phase 1, and the QQ official sender are complete. QQ official
bot C2C messaging is the only V1 notification channel.

A real smoke test using the frozen binding probe confirmed Access Token acquisition, HTTP 200
with QQ API success, and receipt by the bound QQ user.

Notification intents are stored durably with their item-state transition, and only SENT alerts
become improvement baselines. The QQ sender sends the persisted alert snapshot through the HTTP
active C2C API. Access Tokens are cached according to the server's `expires_in` and refreshed
within 60 seconds of expiry. Delivery remains at-least-once: the process may resend a PENDING
alert if QQ accepted it but the process stopped before SQLite recorded SENT.

Production requires three environment variables:

```bash
export QQ_BOT_APP_ID='your-app-id'
export QQ_BOT_USER_OPENID='your-user-openid'
read -rsp 'QQ AppSecret: ' QQ_BOT_APP_SECRET
printf '\n'
export QQ_BOT_APP_SECRET
```

Never commit or log the AppSecret. The program stops at startup if any required QQ credential
is missing.

Run the monitor after setting the environment variables:

```sh
go run ./cmd/monitor -db ./steam-knife-monitor.db
```

Conservative V1 runtime defaults are a 24-hour full scan, 60-second due poll, 2-second page
delay, 1-second detail-request delay, and 5-minute ordinary-error delay. These are operating
defaults, not smis.club official limits.

Run tests:

```sh
go test ./...
go vet ./...
```
