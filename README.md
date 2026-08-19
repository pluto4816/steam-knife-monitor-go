# steam-knife-monitor-go

A clean Go rewrite of the single-process Steam/CS2 knife-opportunity monitor.

Go Core and Collector are complete. The Collector has passed a low-frequency live API smoke
test. The project is currently defining the minimal SQLite persistence; implementation has not
started.

Run tests:

```sh
go test ./...
go vet ./...
```
