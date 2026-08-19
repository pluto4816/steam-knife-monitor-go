# steam-knife-monitor-go

A clean Go rewrite of the single-process Steam/CS2 knife-opportunity monitor.

Go Core, Collector, and SQLite persistence are complete. The current App Loop phase handles
one valid market item at a time; the long-running loop and notifications have not started.

Run tests:

```sh
go test ./...
go vet ./...
```
