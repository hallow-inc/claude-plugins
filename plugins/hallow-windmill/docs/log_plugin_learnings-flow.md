# `f/shared/log_plugin_learnings` — flow spec

Records `hallow-windmill` plugin learnings into the DuckLake catalog so we can analyze drain cadence, topic frequency, and routing decisions over time.

Fired by `/hallow-windmill:wmill-drain-learnings` at end of a drain (Step 5.5) with a batched array of promoted entries. Best-effort — drain does not block on flow success.

## Identity

- **Path:** `f/shared/log_plugin_learnings`
- **Kind:** flow
- **Callable by:** any pusher (folder `f/shared/` is `g/all` readable). Folder ACL gates trigger lookups; here we're calling via MCP `runFlowByPath`, no trigger.
- **`permissioned_as`:** `u/sandbox` if the DuckLake catalog connection requires admin-only resource. If the `dl` ducklake resource is in a `g/all`-readable folder, no elevation needed.

## Input schema

```json
{
  "drained_at": "2026-05-28T14:33:00Z",
  "drained_by": "brandon@hallow.app",
  "entries": [
    {
      "captured_at": "2026-05-24",
      "topic": "HTTP trigger permissioned_as is read-only",
      "observation": "Local YAML permissioned_as is overwritten on push.",
      "evidence": "Job 01ABC... shows server-set value diverges from local YAML.",
      "tags": ["trigger-schema", "permissioned_as"],
      "promoted_to": ["skills/triggers/SKILL.md", "docs/patterns.md §5"],
      "status": "promoted"
    }
  ]
}
```

`status` is currently always `promoted` — skipped entries are not sent.

## DuckLake table

```sql
ATTACH 'ducklake' AS dl;

CREATE SCHEMA IF NOT EXISTS dl.dev_docs;

CREATE TABLE IF NOT EXISTS dl.dev_docs.plugin_learnings (
  drained_at        TIMESTAMP    NOT NULL,
  drained_by        VARCHAR      NOT NULL,
  captured_at       DATE,
  topic             VARCHAR      NOT NULL,
  observation       VARCHAR,
  evidence          VARCHAR,
  tags              VARCHAR[],
  promoted_to       VARCHAR[],
  status            VARCHAR      NOT NULL DEFAULT 'promoted',
  inserted_at       TIMESTAMP    DEFAULT CURRENT_TIMESTAMP
);
```

## Flow modules

Two steps. The DuckDB step needs `tag: fargate` if the DuckLake catalog reads/writes the sandbox S3 bucket (see write-script-duckdb skill, "Sandbox-bucket DuckLake jobs require `tag: fargate`"). If catalog is fully Postgres-backed, default worker is fine.

### Step 1 — `provision` (DuckDB)

```sql
ATTACH 'ducklake' AS dl;
CREATE SCHEMA IF NOT EXISTS dl.dev_docs;
CREATE TABLE IF NOT EXISTS dl.dev_docs.plugin_learnings (
  drained_at  TIMESTAMP    NOT NULL,
  drained_by  VARCHAR      NOT NULL,
  captured_at DATE,
  topic       VARCHAR      NOT NULL,
  observation VARCHAR,
  evidence    VARCHAR,
  tags        VARCHAR[],
  promoted_to VARCHAR[],
  status      VARCHAR      NOT NULL DEFAULT 'promoted',
  inserted_at TIMESTAMP    DEFAULT CURRENT_TIMESTAMP
);
SELECT 1 AS ok;
```

Idempotent. Runs on every invocation; succeeds whether the table existed or not.

### Step 2 — `insert_batch` (python3 + duckdb)

Receives the flow input `entries[]` plus `drained_at` / `drained_by`. Uses parameterized inserts (DuckDB's `$name` binding does not allow list literals in some positions — pass tags / promoted_to as JSON strings and `json_extract` server-side, or unnest array literals per row).

```python
import duckdb
import wmill
from datetime import datetime

def main(drained_at: str, drained_by: str, entries: list):
    con = duckdb.connect()
    con.execute("ATTACH 'ducklake' AS dl")
    for e in entries:
        con.execute(
            """
            INSERT INTO dl.dev_docs.plugin_learnings
              (drained_at, drained_by, captured_at, topic, observation, evidence, tags, promoted_to, status)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            [
                drained_at,
                drained_by,
                e.get("captured_at"),
                e["topic"],
                e.get("observation"),
                e.get("evidence"),
                e.get("tags", []),
                e.get("promoted_to", []),
                e.get("status", "promoted"),
            ],
        )
    return {"inserted": len(entries)}
```

Hallow rule: prefer python3 + `import duckdb` over the DuckDB script runtime for non-trivial work (see write-script-duckdb skill, "DuckDB should be Python").

## Provisioning

Until this flow exists in the `dev` workspace, `/wmill-drain-learnings` will log a warning and skip the call. Admin steps to provision:

1. Push the flow via `wmill flow push f/shared/log_plugin_learnings --workspace dev` (or create via UI). Push must come from `u/sandbox` if `permissioned_as` elevation is needed.
2. Verify the DuckLake resource path the step ATTACHes — adjust if the catalog name is not `ducklake`.
3. First drain after provisioning will create the table via Step 1.

## Failure behavior

Drain is best-effort. If `runFlowByPath` returns error or the flow path does not exist, drain logs:

```
[warn] log_plugin_learnings flow call failed: <err> — see docs/log_plugin_learnings-flow.md to provision.
```

Drain still reports success on the local file edits. The learnings table is observability, not a transactional store.
