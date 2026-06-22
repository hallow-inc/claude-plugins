---
name: resources
description: Use when creating, reading, or wiring a Windmill **resource** — typed credential/connection (DB, API, S3, OAuth) stored in the workspace and referenced from scripts/flows. Triggers on "add a resource", "database credentials in windmill", "resource type", `.resource.yaml`, `.resource-type.yaml`, "change resource_type", "resource_type immutable", "apify Cloud only", "linked secret variable", "auto-linked variable", "apify_api_key". Covers resource types, secret fields, referencing from script main signatures, `resource_type` immutability (delete-recreate procedure), two flavors of secret storage (standalone `f/platform_secrets/<domain>__<name>` vs auto-linked at resource path), Cloud-only OAuth RTs vs `*_api_key` self-host variants (apify).
---

# Windmill Resources

Resources store credentials and configuration for external services.

## File Format

Resource files use the pattern: `{path}.resource.json`

Example: `f/databases/postgres_prod.resource.json`

## Resource Structure

```json
{
  "value": {
    "host": "db.example.com",
    "port": 5432,
    "user": "admin",
    "password": "$var:g/all/db_password",
    "dbname": "production"
  },
  "description": "Production PostgreSQL database",
  "resource_type": "postgresql"
}
```

## Required Fields

- `value` - Object containing the resource configuration
- `resource_type` - Name of the resource type (e.g., "postgresql", "slack")

## Variable References

Reference variables in resource values:

```json
{
  "value": {
    "api_key": "$var:g/all/api_key",
    "secret": "$var:f/platform_secrets/myservice__secret"
  }
}
```

**Reference formats:**
- `$var:g/all/name` - Global variable
- `$var:u/username/name` - User variable
- `$var:f/folder/name` - Folder variable

## Resource References

Reference other resources:

```json
{
  "value": {
    "database": "$res:f/databases/postgres"
  }
}
```

## Common Resource Types

### PostgreSQL
```json
{
  "resource_type": "postgresql",
  "value": {
    "host": "localhost",
    "port": 5432,
    "user": "postgres",
    "password": "$var:g/all/pg_password",
    "dbname": "windmill",
    "sslmode": "prefer"
  }
}
```

### DuckLake catalog (Hallow, `resource_type: postgresql`)

Three pre-provisioned PG resources pointing at the shared DuckLake catalog. Don't create new ones — pick by intent. Full ruleset in `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` §9b.

| Resource path | PG role | Use for |
|---|---|---|
| `f/shared/ducklake_catalog_ro` | `lake_reader` | Read-only queries from any folder. **Default for analytics scripts.** |
| `f/<dept>/ducklake_catalog` | `lake_<dept>` | Department writer. Reads any schema, writes only `dl.<dept>.*`. ACL on `f/<dept>/` gates who can use. |
| `f/platform/ducklake/catalog_pg` | `postgres` | Admin superuser. Platform maintenance + schema provisioning only. |

All three target `127.0.0.1:5435` (tsforwarder sidecar). They only resolve from scripts/flows tagged `fargate`. Pair with `from f.platform.ducklake.lib import connect` in a Python script — see `write-script-python3` SKILL.md "DuckLake" section.

### MySQL
```json
{
  "resource_type": "mysql",
  "value": {
    "host": "localhost",
    "port": 3306,
    "user": "root",
    "password": "$var:g/all/mysql_password",
    "database": "myapp"
  }
}
```

### Slack
```json
{
  "resource_type": "slack",
  "value": {
    "token": "$var:g/all/slack_token"
  }
}
```

### AWS S3
```json
{
  "resource_type": "s3",
  "value": {
    "bucket": "my-bucket",
    "region": "us-east-1",
    "accessKeyId": "$var:g/all/aws_access_key",
    "secretAccessKey": "$var:g/all/aws_secret_key"
  }
}
```

### HTTP/API
```json
{
  "resource_type": "http",
  "value": {
    "baseUrl": "https://api.example.com",
    "headers": {
      "Authorization": "Bearer $var:g/all/api_token"
    }
  }
}
```

### Kafka
```json
{
  "resource_type": "kafka",
  "value": {
    "brokers": "broker1:9092,broker2:9092",
    "sasl_mechanism": "PLAIN",
    "security_protocol": "SASL_SSL",
    "username": "$var:g/all/kafka_user",
    "password": "$var:g/all/kafka_password"
  }
}
```

### NATS
```json
{
  "resource_type": "nats",
  "value": {
    "servers": ["nats://localhost:4222"],
    "user": "$var:g/all/nats_user",
    "password": "$var:g/all/nats_password"
  }
}
```

### MQTT
```json
{
  "resource_type": "mqtt",
  "value": {
    "host": "mqtt.example.com",
    "port": 8883,
    "username": "$var:g/all/mqtt_user",
    "password": "$var:g/all/mqtt_password",
    "tls": true
  }
}
```

## Custom Resource Types

Create custom resource types with JSON Schema:

```json
{
  "name": "custom_api",
  "schema": {
    "type": "object",
    "properties": {
      "base_url": {"type": "string", "format": "uri"},
      "api_key": {"type": "string"},
      "timeout": {"type": "integer", "default": 30}
    },
    "required": ["base_url", "api_key"]
  },
  "description": "Custom API connection"
}
```

Save as: `custom_api.resource-type.json`

## OAuth Resources

OAuth resources are managed through the Windmill UI and marked:

```json
{
  "is_oauth": true,
  "account": 123
}
```

OAuth tokens are automatically refreshed by Windmill.

## Using Resources in Scripts

### TypeScript (Bun/Deno)
```typescript
export async function main(db: RT.Postgresql) {
  // db contains the resource values
  const { host, port, user, password, dbname } = db;
}
```

### Python
```python
class postgresql(TypedDict):
    host: str
    port: int
    user: str
    password: str
    dbname: str

def main(db: postgresql):
    # db contains the resource values
    pass
```

## CLI Commands

```bash
# List resources
wmill resource list

# List resource types with schemas
wmill resource-type list --schema

# Get specific resource type schema
wmill resource-type get postgresql

```

**Hallow ban:** `wmill sync push` and `wmill sync pull` are banned in this workspace. They delete server state not in local files and clobber secret variables. Mirror resource changes to the server via the MCP `windmill` tools or the Windmill UI — never `wmill sync`.

## Hallow gotchas (resources)

### `resource_type` is immutable — push silently keeps old type

After a resource is created, its `resource_type` field cannot be changed. `wmill resource push` / `sync push` will REPORT success on a type change but silently keep the original type (only `value` + `description` actually update). The CLI has no `wmill resource delete` — and the MCP `deleteResource` is bound to the `admins` workspace only, unusable for `dev` / `prod`.

**Procedure to change a resource type:**
1. `DELETE /api/w/<ws>/resources/delete/<path>` via raw HTTP with a workspace token (UI delete may not commit due to caching — prefer the raw API).
2. Re-create the resource with the new `resource_type` via the API.
3. Verify with `wmill resource get <path>` that `resource_type` matches.

### Resource-backed secrets auto-create a linked variable at the SAME path

When a resource type has a secret-typed field, Windmill auto-creates a backing variable at the **resource's own path**, marked `is_linked: true`. Do NOT manually create a variable at that path — it collides with the auto-linked one.

Two secret-storage flavors coexist in Hallow:

| Flavor | When | Naming |
|---|---|---|
| Standalone secret var | Cross-domain shared secret (e.g. `db_password` used by multiple resources) | `f/platform_secrets/<domain>__<name>` (double underscore) |
| Resource-backed secret | Single resource owns the secret | Auto-linked at resource path; do NOT pre-create |

### `apify` resource-type is Cloud-only OAuth — self-host uses `apify_api_key`

The `apify` RT in the `admins` workspace is OAuth-only ("Available only on Windmill Cloud") — self-hosted Hallow can't seed it. Use the sibling `apify_api_key` RT instead (`{api_key}` schema).

**General rule for self-host:** when an n8n / generic doc refers to an OAuth resource type, look for a sibling `*_api_key` variant first. OAuth-only RTs to watch for on self-host: `apify` (use `apify_api_key`). RTs that work on self-host because they accept a plain token field: `gdrive`, `gsheets`, `gcal`, `gmail` (paste OAuth token manually).

### Google OAuth-connect now works on Hallow's fork (gcal, gmail, gdrive, …) — UI Connect button, not just manual paste

Hallow's customized-OSS fork enabled the full OSS OAuth-**connect** flow for any resource type in the `oauth_connect.json` registry (`deviation: oauth connect for gcal and gmail as resources`, plus `deviation: implement OSS OAuth/SSO login`). So for `gcal`, `gmail`, `gdrive`, `gsheets` etc. you can now use the Windmill UI **"Connect"** button (instance OAuth app must be configured by an admin) — it runs the authorize→callback flow, persists an `account` row + refresh token, and Windmill auto-refreshes it. You no longer HAVE to hand-paste a token.

- `gcal` scope: `https://www.googleapis.com/auth/calendar.events`. `gmail` scope: `https://www.googleapis.com/auth/gmail.send`.
- This is the OSS connect flow (stock OSS gates it behind EE/Cloud). It needs the instance-level Google OAuth client configured in instance settings (admin task) before the Connect button works.
- Manual-paste still works as a fallback for any of these RTs if the OAuth client isn't configured. `apify` is still OAuth-Cloud-only at the RT level — use `apify_api_key`.
