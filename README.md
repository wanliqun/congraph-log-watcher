# graph-log-watcher

Build with `go build ./cmd/graph-log-watcher`. Validate configuration with `graph-log-watcher check-config --config config/example.yaml`; begin deployment with `run --config config/example.yaml --dry-run` for 24–48 hours before enabling notifications.

Docker Engine socket access is highly privileged. The watcher only uses Docker list, inspect, logs, and events APIs, but the socket should be mounted only for this dedicated service. Persist checkpoint state at `/var/lib/graph-log-watcher`; expose `/metrics` and `/healthz` on port 9108. Pin production images by digest.
