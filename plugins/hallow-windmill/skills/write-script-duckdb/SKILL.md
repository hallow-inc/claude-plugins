---
name: write-script-duckdb
description: Use when writing a Windmill DuckDB script — in-process analytical SQL via the `duckdb` runtime. Triggers on "duckdb query", local-file analytics, CSV/Parquet inspection, "DuckLake catalog", "ATTACH ducklake", "duckdb $name bind", "duckdb %%name%% literal substitution", "DuckDB rejected bucket name with dash", "DuckLake on S3 fails". Covers attach syntax, parameter binding ($name vs %%name%% restrictions — both reject S3 paths in common positions), result shape, DuckLake catalog file CANNOT live on S3 (use Postgres/MySQL catalog), Hallow rule "DuckDB should be Python" (prefer python3 script + `import duckdb` for non-trivial work), sandbox-bucket jobs require `tag: fargate`.
---

> **CLI lifecycle** (preview vs run, mirror via MCP, never `wmill sync`): see `${CLAUDE_PLUGIN_ROOT}/skills/cli-commands/references/preview-vs-run.md`.

> **🛑 Hallow rule: DuckLake work uses Python, not DuckDB script kind.** If the user wants DuckLake (`dl.<schema>.<table>`, `ATTACH 'ducklake:...'`, etc.), STOP — switch to `write-script-python3`. The DuckDB script kind's parameter binding gates reject S3 paths, so it cannot drive DuckLake reliably. See `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` §9b. This skill remains valid only for one-off local CSV/Parquet analytics with no S3/DuckLake involvement.

# DuckDB

Arguments are defined with comments and used with `$name` syntax:

```sql
-- $name (text) = default
-- $age (integer)
SELECT * FROM users WHERE name = $name AND age > $age;
```

## Ducklake Integration

Attach Ducklake for data lake operations:

```sql
-- Main ducklake
ATTACH 'ducklake' AS dl;

-- Named ducklake
ATTACH 'ducklake://my_lake' AS dl;

-- Then query
SELECT * FROM dl.schema.table;
```

## External Database Connections

Connect to external databases using resources:

```sql
ATTACH '$res:path/to/resource' AS db (TYPE postgres);
SELECT * FROM db.schema.table;
```

## S3 File Operations

Read files from S3 storage:

```sql
-- Default storage
SELECT * FROM read_csv('s3:///path/to/file.csv');

-- Named storage
SELECT * FROM read_csv('s3://storage_name/path/to/file.csv');

-- Parquet files
SELECT * FROM read_parquet('s3:///path/to/file.parquet');

-- JSON files
SELECT * FROM read_json('s3:///path/to/file.json');
```

### Receiving an S3Object as a script parameter

Declare the arg with type `(s3object)`. Windmill renders an S3 file picker for it
and binds the arg as the bare `s3://storage/key` URI, which DuckDB's reader
functions consume directly:

```sql
-- $file (s3object)
SELECT * FROM read_parquet($file);
```

Works with any DuckDB reader: `read_csv($file)`, `read_json($file)`, etc.

### Writing query results to S3

DuckDB writes to S3 natively via `COPY ... TO`:

```sql
COPY (SELECT * FROM users) TO 's3:///exports/users.parquet' (FORMAT PARQUET);
```

Use this instead of the `-- s3` streaming directive supported by the other SQL
dialects — that directive is not available in DuckDB.

## Hallow gotchas (DuckDB)

### Prefer python3 + `import duckdb` for non-trivial DuckDB work

User policy: **DuckDB should be Python**. The `duckdb` script kind has hard parser/parameter restrictions (below) that make S3 paths and dynamic table/bucket names painful. Use the `write-script-python3` skill instead and import the `duckdb` library:

```python
import duckdb

def main(bucket: str, key: str):
    con = duckdb.connect()
    con.execute("INSTALL httpfs; LOAD httpfs;")
    return con.execute(f"SELECT * FROM read_parquet('s3://{bucket}/{key}')").fetchall()
```

The `duckdb` script kind here is for **simple** SQL-only analytics where the parameters fit its model. Reach for python3 + `duckdb` library the moment you hit either restriction below.

### Parameter syntax: `$name` and `%%name%%` both have hard restrictions

The DuckDB script kind exposes two parameter modes — both reject S3 paths in common positions:

| Syntax | Mode | Restriction |
|---|---|---|
| `$name` | Bind parameter (prepared-statement style) | Parser REJECTS bind params in literal-only positions like `COPY ... TO '...'` and `ATTACH '...'` — DuckDB's grammar requires a string literal there. |
| `%%name%%` | Pre-parse literal substitution | Substitution is hard-gated to identifier-shape values: `[A-Za-z_][A-Za-z0-9_]*`. Bucket names with `-` (the Hallow sandbox bucket has dashes) are REJECTED. No override flag. |

If you need a dynamic S3 path: use python3 + `import duckdb` and build the SQL string in code.

### DuckLake catalog file CANNOT live on S3

`ATTACH 'ducklake:s3://.../catalog.ducklake' (CREATE_IF_NOT_EXISTS true)` FAILS with `"database does not exist"`. S3 is not a random-write filesystem, and the DuckLake catalog needs in-place updates.

**Hallow implication:** every DuckLake on this platform must use either:
- A **Postgres or MySQL catalog** (recommended — durable, multi-worker-safe).
- A **local DuckDB file on a worker with disk** — but Fargate workers are ephemeral, so the file dies with the task. Only viable for one-shot batch jobs that finish in a single run.

### Sandbox-bucket DuckLake jobs require `tag: fargate`

Same constraint as any S3 step: the EC2-hosted `default` worker has no IAM grant to the sandbox bucket. Wrap DuckLake jobs in a flow with `tag: fargate` on the module. See the write-flow skill's "S3 step MUST set `tag: fargate`" section for the masked-error symptom.

### `wmill generate-metadata` doesn't see hand-written script files

If you wrote a `.sql` file by hand, `wmill generate-metadata` silently emits "no scripts found" until you bootstrap it first (`wmill script bootstrap <path> duckdb`, then overwrite, then generate-metadata). See `${CLAUDE_PLUGIN_ROOT}/skills/cli-commands/SKILL.md` "Hallow gotchas → wmill generate-metadata doesn't discover hand-written script files" for the full procedure.
