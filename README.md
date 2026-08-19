# steam-knife-monitor-go

A clean Go rewrite of the single-process Steam/CS2 knife-opportunity monitor.

Go Core, Collector, SQLite persistence, Scan Cycle, Detail Market Fetch, and Tier Scheduler are
complete. The current phase is the long-running runtime; notifications have not started.

Conservative V1 runtime defaults are a 24-hour full scan, 60-second due poll, 2-second page
delay, 1-second detail-request delay, and 5-minute ordinary-error delay. These are operating
defaults, not smis.club official limits.

Run tests:

```sh
go test ./...
go vet ./...
```
