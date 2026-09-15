# graph-log-watcher

`graph-log-watcher` follows graph-node Docker logs and turns repeated or critical events into deduplicated DingTalk incidents. It is an event detector, not a replacement for Prometheus state alerts or Loki.

## Build and test

Go 1.23 or newer is required.

```sh
go build ./cmd/graph-log-watcher
go test ./...
go test -race ./...
go vet ./...
```

Validate configuration and start in dry-run mode:

```sh
export DINGTALK_WEBHOOK='https://oapi.dingtalk.com/robot/send?access_token=...'
export DINGTALK_SECRET='SEC...'
./graph-log-watcher check-config --config config/example.yaml
./graph-log-watcher run --config config/example.yaml --dry-run
```

Dry run executes parsing, redaction, matching, windowing, deduplication, cooldown, context capture and checkpointing, but prints `WOULD ALERT` instead of contacting DingTalk. Run this for 24 to 48 hours, start with the critical rule, and tune specific rules before enabling generic ones. Remove `--dry-run` only after reviewing alert volume.

## Docker Compose

```sh
DINGTALK_WEBHOOK='...' DINGTALK_SECRET='...' docker compose -f deploy/docker-compose.yaml up --build
```

The Compose service mounts configuration read-only, persists bbolt state in `watcher-state`, and exposes port 9108. `/healthz` reports prolonged Docker API failure, all target containers being detached, and checkpoint failures. `/metrics` exposes the `graph_log_watcher_*` counters and gauges documented in the design.

The Docker socket grants highly privileged host access. Mount it only into this dedicated service. The watcher code restricts itself to container list, inspect, logs and events APIs, but the socket itself cannot enforce that restriction. Pin production images by digest.

## Operations

Checkpoint state defaults to `/var/lib/graph-log-watcher/state.db`. Restart replay uses an overlap and a timestamp plus event-hash marker, providing at-least-once handling without persisting raw logs. A SIGINT or SIGTERM stops Docker streams, drains accepted logs, flushes checkpoints, drains the notification queue within `shutdown.timeout`, and then closes HTTP and bbolt.

Keep rules ordered from specific to generic and use `stop_on_match` where categories overlap. Prefer structured fields for fingerprints, normalize volatile IDs and numbers, and keep redaction rules ahead of any context or incident state. A 503 from `/healthz`, rising notifier errors, reconnects, or a stale checkpoint gauge should be investigated before changing alert thresholds.
