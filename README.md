# Spectr

> Agent observability engine — purpose-built telemetry for AI coding agents.

⚠️ **Early development — not production ready.**

## Status

| Component | Status | Notes |
|-----------|--------|-------|
| SQLite Event Store | ✅ Working | WAL mode, migrations, 87.8% coverage |
| REST API | ✅ Working | 10 endpoints + WebSocket streaming |
| OpenClaw Collector | ✅ Working | JSONL ingestion, fsnotify, offset tracking |
| Claude Code Collector | ✅ Working | JSONL parser, token extraction |
| Codex CLI Collector | ✅ Working | Session meta, turn-level tracking |
| Drift Detection | ✅ Working | Z-score baseline, loop detection |
| Cost/Pricing | ⚠️ WIP | Per-token only — needs subscription tier support |
| Dashboard | ⚠️ WIP | Renders but needs design pass, summary endpoints missing |
| Token Tracking | ⚠️ WIP | Shows 0 for OpenClaw (JSONL lacks usage data, needs OTel) |
| Alerting | 🔲 Not started | Telegram/webhook/email |
| Embedding SDK | 🔲 Not started | One-line integration for Go/Node/Python |
| Documentation | 🔲 Not started | |

## Quick Start

```bash
go build -o bin/spectr ./cmd/spectr
./bin/spectr
# → http://localhost:9099
```

## Docker

```bash
docker build -t spectr:dev .
docker run -p 9099:9099 spectr:dev
```

Image size: ~11MB (scratch base).

## Architecture

Single Go binary. SQLite WAL for storage. Zero external dependencies.

```
spectr binary
├── Collectors (OpenClaw, Claude Code, Codex CLI)
│   └── fsnotify + JSONL parsing → Store
├── Drift Detection
│   └── Z-score baseline + loop detection → Alerts
├── REST API + WebSocket
│   └── /api/v1/* + /ws/stream
└── Dashboard (embedded HTMX)
    └── Served at /
```

## License

Apache 2.0
