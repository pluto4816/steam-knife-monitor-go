# steam-knife-monitor-go

A clean Go rewrite of the single-process Steam/CS2 knife-opportunity monitor.

Go Core, Collector, SQLite persistence, Scan Cycle, and Detail Market Fetch are complete.
The current phase is Tier Scheduler; the long-running loop and notifications have not started.

Run tests:

```sh
go test ./...
go vet ./...
```
