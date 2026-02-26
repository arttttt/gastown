# Dolt Storage Architecture

> **Status**: Current reference for Gas Town agents
> **Updated**: 2026-02-16
> **Context**: Dolt is the sole storage backend for Beads and Gas Town

---

## Overview

Gas Town uses [Dolt](https://github.com/dolthub/dolt), an open-source
SQL database with Git-like versioning (Apache 2.0). One Dolt SQL server
per town serves all databases via MySQL protocol on port 3307. There is
no embedded mode, no SQLite, and no JSONL.

The `gt daemon` manages the server lifecycle (auto-start, health checks
every 30s, crash restart with exponential backoff).

## Server Architecture

```
Dolt SQL Server (one per town, port 3307)
├── hq/       town-level beads  (hq-* prefix)
├── gastown/  rig beads         (gt-* prefix)
├── beads/    rig beads         (bd-* prefix)
└── ...       additional rigs
```

**Data directory**: `~/.dolt-data/` — each subdirectory is a database
accessible via `USE <name>` in SQL.

**Connection**: `root@tcp(127.0.0.1:3307)/<database>` (no password for
localhost).

## Commands

```bash
# Daemon manages server lifecycle (preferred)
gt daemon start

# Manual management
gt dolt start          # Start server
gt dolt stop           # Stop server
gt dolt status         # Health check, list databases
gt dolt logs           # View server logs
gt dolt sql            # Open SQL shell
gt dolt init-rig <X>   # Create a new rig database
gt dolt list           # List all databases
```

If the server isn't running, `bd` fails fast with a clear message
pointing to `gt dolt start`.

## Write Concurrency: All-on-Main

All agents — polecats, crew, witness, refinery, deacon — write directly
to `main`. Concurrency is managed through transaction discipline: every
write wraps `BEGIN` / `DOLT_COMMIT` / `COMMIT` atomically.

```
bd update <bead> --status=in_progress
  → BEGIN
  → UPDATE issues SET status='in_progress' ...
  → CALL DOLT_COMMIT('-Am', 'update status')
  → COMMIT
```

This eliminates the former branch-per-worker strategy (BD_BRANCH,
per-polecat Dolt branches, merge-at-done). All writes are immediately
visible to all agents — no cross-agent visibility gaps.

Multi-statement `bd` commands batch their writes inside a single
transaction to maintain atomicity.

## Schema

```sql
CREATE TABLE issues (
    id VARCHAR(64) PRIMARY KEY,
    type VARCHAR(32),
    title TEXT,
    description TEXT,
    status VARCHAR(32),
    priority INT,
    owner VARCHAR(255),
    assignee VARCHAR(255),
    labels JSON,
    parent VARCHAR(64),
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    closed_at TIMESTAMP
);

CREATE TABLE mail (
    id VARCHAR(64) PRIMARY KEY,
    thread_id VARCHAR(64),
    from_addr VARCHAR(255),
    to_addrs JSON,
    subject TEXT,
    body TEXT,
    sent_at TIMESTAMP,
    read_at TIMESTAMP
);

CREATE TABLE channels (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255),
    type VARCHAR(32),
    config JSON,
    created_at TIMESTAMP
);
```

## Dolt-Specific Capabilities

These are available to agents via SQL and used throughout Gas Town:

| Feature | Usage |
|---------|-------|
| `dolt_history_*` tables | Full row-level history, queryable via SQL |
| `AS OF` queries | Time-travel: "what did this look like yesterday?" |
| `dolt_diff()` | "What changed between these two points?" |
| `DOLT_COMMIT` | Explicit commit with message (auto-commit is the default) |
| `DOLT_MERGE` | Merge branches (integration branches, federation) |
| `dolt_conflicts` table | Programmatic conflict resolution after merge |
| `DOLT_BRANCH` | Create/delete branches (integration branches) |

**Auto-commit** is on by default: every write gets a Dolt commit. Agents
can batch writes by disabling auto-commit temporarily.

**Conflict resolution** default: `newest` (most recent `updated_at` wins).
Arrays (labels): `union` merge. Counters: `max`.

## Three Data Planes

Beads data falls into three planes with different characteristics:

| Plane | What | Mutation | Durability | Transport |
|-------|------|----------|------------|-----------|
| **Operational** | Work in progress, status, assignments, heartbeats | High (seconds) | Days–weeks | Dolt SQL server (local) |
| **Ledger** | Completed work, permanent record, skill vectors | Low (completion boundaries) | Permanent | DoltHub remotes + federation |
| **Design** | Epics, RFCs, specs — ideas not yet claimed | Conversational | Until crystallized | DoltHub commons (shared) |

The operational plane lives entirely in the local Dolt server. The ledger
and design planes federate via DoltHub using the Highway Operations
Protocol — Gas Town's public federation layer built on Dolt's native
push/pull remotes.

## Standalone Beads Note

The `bd` CLI retains an embedded Dolt option for standalone use (outside
Gas Town). Server-only mode applies to Gas Town exclusively — standalone
users may not have a Dolt server running.

## Remote Push (Git Protocol)

Gas Town pushes Dolt databases to GitHub remotes via `gt dolt sync`. These
use git SSH protocol (`git+ssh://git@github.com/...`), not DoltHub's native
protocol.

### Git Remote Cache

Dolt maintains a cache at `.dolt-data/<db>/.dolt/git-remote-cache/` that
stores git objects built from Dolt's internal format. Per the Dolt team
(Dustin Brown, 2026-02-26):

- **The cache is necessary** — Dolt uses it to build git objects for push/pull
- **Accumulates garbage** (orphaned refs) and is not cleaned up automatically
- **Safe to delete** between pushes, but causes a full rebuild on next push
  (beads: ~20 min rebuild, gastown: even longer)
- **Orphaned refs** can be pruned without deleting the whole cache — better balance
- **Grows over time** as the database grows — inherent to git-protocol remotes

**Guidance**: Do NOT routinely delete the cache. Prefer pruning orphaned refs.
Full deletion should only be done when disk pressure is critical and a long
rebuild is acceptable.

### Sync Procedure

`gt dolt sync` parks all rigs (stops witnesses/refineries), stops the Dolt
server, runs `dolt push` for each database with a configured remote, then
restarts the server and unparks rigs. The parking prevents witnesses from
detecting the server outage and restarting it mid-push.

### Force Push

After data recovery (e.g., Clown Show #13), local and remote histories
diverge. Use `gt dolt sync --force` for the first push to overwrite the
remote with local state. Subsequent pushes should work without `--force`.

### Known Limitations

- **Slow**: Git-protocol remotes are orders of magnitude slower than DoltHub
  native remotes. A 71MB database takes ~90s; larger ones take 20+ minutes.
- **Cache growth**: No automatic garbage collection. Orphan pruning TBD.
- **Server downtime**: Push requires exclusive access to the data directory,
  so the server must be stopped during push. This creates a maintenance window.

### Future: DoltHub Remotes

DoltHub's native protocol (`https://doltremoteapi.dolthub.com/...`) avoids
the git-remote-cache entirely and is much faster. Migration requires DoltHub
accounts and reconfiguring remotes with `dolt remote set-url`.

## File Layout

```
~/gt/                            Town root
├── .dolt-data/                  Centralized Dolt data directory
│   ├── hq/                      Town beads (hq-*)
│   ├── gastown/                 Gastown rig (gt-*)
│   ├── beads/                   Beads rig (bd-*)
│   └── wyvern/                  Wyvern rig (wy-*)
├── daemon/
│   ├── dolt.pid                 Server PID (daemon-managed)
│   ├── dolt-server.log          Server log
│   └── dolt-state.json          Server state
└── mayor/
    └── daemon.json              Daemon config (dolt_server section)
```
