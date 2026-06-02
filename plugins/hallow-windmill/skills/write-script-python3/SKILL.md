---
name: write-script-python3
description: Use when writing a Windmill Python script (`.py`, python3 runtime). Triggers on "python script in windmill", `def main(...)`, `import wmill`, pip requirements in script header. Covers main signature, wmill SDK usage, dependency pinning, and wmill CLI lifecycle.
---

> **CLI lifecycle** (preview vs run, mirror via MCP, never `wmill sync`): see `${CLAUDE_PLUGIN_ROOT}/skills/cli-commands/references/preview-vs-run.md`.

# Python

## Structure

The script must contain at least one function called `main`:

```python
def main(param1: str, param2: int):
    # Your code here
    return {"result": param1, "count": param2}
```

Do not call the main function. Libraries are installed automatically.

## Resource Types

On Windmill, credentials and configuration are stored in resources and passed as parameters to main.

You need to **redefine** the type of the resources that are needed before the main function as TypedDict:

```python
from typing import TypedDict

class postgresql(TypedDict):
    host: str
    port: int
    user: str
    password: str
    dbname: str

def main(db: postgresql):
    # db contains the database connection details
    pass
```

**Important rules:**

- The resource type name must be **IN LOWERCASE**
- Only include resource types if they are actually needed
- If an import conflicts with a resource type name, **rename the imported object, not the type name**
- Make sure to import TypedDict from typing **if you're using it**

## Imports

Libraries are installed automatically. Do not show installation instructions.

```python
import requests
import pandas as pd
from datetime import datetime
```

If an import name conflicts with a resource type:

```python
# Wrong - don't rename the type
import stripe as stripe_lib
class stripe_type(TypedDict): ...

# Correct - rename the import
import stripe as stripe_sdk
class stripe(TypedDict):
    api_key: str
```

## Windmill Client

Import the windmill client for platform interactions:

```python
import wmill
```

See the SDK documentation for available methods.

## Preprocessor Scripts

For preprocessor scripts, the function should be named `preprocessor` and receives an `event` parameter:

```python
from typing import TypedDict, Literal, Any

class Event(TypedDict):
    kind: Literal["webhook", "http", "websocket", "kafka", "email", "nats", "postgres", "sqs", "mqtt", "gcp"]
    body: Any
    headers: dict[str, str]
    query: dict[str, str]

def preprocessor(event: Event):
    # Transform the event into flow input parameters
    return {
        "param1": event["body"]["field1"],
        "param2": event["query"]["id"]
    }
```

## S3 Object Operations

Windmill provides built-in support for S3-compatible storage operations.

### Receiving an S3Object as a script parameter

To accept a file from S3 as input to a script, type the parameter with `S3Object` (imported from `wmill`):

```python
import wmill
from wmill import S3Object

def main(file: S3Object):
    content = wmill.load_s3_file(file)
    # ...
```

### S3 operations

```python
import wmill

# Load file content from S3
content: bytes = wmill.load_s3_file(s3object)

# Load file as stream reader
reader: BufferedReader = wmill.load_s3_file_reader(s3object)

# Write file to S3
result: S3Object = wmill.write_s3_file(
    s3object,           # Target path (or None to auto-generate)
    file_content,       # bytes or BufferedReader
    s3_resource_path,   # Optional: specific S3 resource
    content_type,       # Optional: MIME type
    content_disposition # Optional: Content-Disposition header
)
```


## Python SDK (wmill)

Full method reference at `${CLAUDE_PLUGIN_ROOT}/skills/write-script-python3/references/sdk.md`. Read it when you need: HTTP client, run_script_*, wait_for_completion, get/set_variable, get/set_resource, get/set_state, load/write_s3_file, get_resume_urls, DataTable/DuckLake clients, workflow-as-code decorators.

Common imports:
```python
import wmill
```

## DuckLake (Hallow canonical analytical store)

Use Python for ALL DuckLake work — the DuckDB script kind is forbidden (parameter binding gates reject S3 paths). See `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` §9b for the full ruleset.

**Minimum viable read script:**

```python
from typing import TypedDict
from f.platform.ducklake.lib import connect

class postgresql(TypedDict):
    host: str; port: int; user: str; password: str; dbname: str

def main(db: postgresql):
    con = connect(db, read_only=True)
    try:
        return con.execute("SELECT * FROM dl.shared.<table> LIMIT 10").fetchall()
    finally:
        con.close()
```

**Hard requirements:**

| Rule | Why |
|---|---|
| Script kind: **Python** | DuckDB kind's `$name` / `%%name%%` binds reject S3 paths |
| Script tag: **`fargate`** | Catalog resource targets `127.0.0.1:5435` — only resolves in Fargate task netns (tsforwarder sidecar). Default/EC2 workers cannot reach it. |
| Import: `from f.platform.ducklake.lib import connect` | Loads ducklake + httpfs + postgres extensions, creates S3 secret, ATTACHes lake as `dl` |
| `db` param resource: pick by intent | `f/shared/ducklake_catalog_ro` (read-only any schema), `f/<dept>/ducklake_catalog` (writes to own schema only), `f/platform/ducklake/catalog_pg` (admin) |
| Write target: **department schema only** | Never `dl.main.*` (reserved). Scripts under `f/finance/` write `dl.finance.*`. Cross-schema writes blocked by lib-level guard. |
| Schema creation | NEVER `CREATE SCHEMA` from a script. Only `f/platform/ducklake/provision_schema` (admin). |
| Maintenance | NEVER `CHECKPOINT` or `CALL ducklake_*` inline. `f/platform/ducklake/maintain` runs CHECKPOINT daily. |

**Script-yaml fields:**

```yaml
summary: <what it does>
description: <how it fits in>
lock: !inline <lock>
content: !inline <content>
schema: { ... }
language: python3
kind: script
tag: fargate          # mandatory for DuckLake
```

Keep imports lean — only pull what you call.
