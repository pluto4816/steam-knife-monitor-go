# steam-knife-monitor-go

A clean Go rewrite of the single-process Steam/CS2 knife-opportunity monitor.

Go Core, Collector, SQLite persistence, Scan Cycle, Detail Market Fetch, Tier Scheduler, and
the long-running runtime are complete. The current phase is Notification.

Notification intents are stored durably with their item-state transition, and only SENT alerts
become improvement baselines. Delivery is at-least-once: without idempotency support from a
future notification provider, SQLite cannot eliminate the narrow duplicate-delivery window
between a successful external send and recording SENT.

Conservative V1 runtime defaults are a 24-hour full scan, 60-second due poll, 2-second page
delay, 1-second detail-request delay, and 5-minute ordinary-error delay. These are operating
defaults, not smis.club official limits.

Run tests:

```sh
go test ./...
go vet ./...
```
