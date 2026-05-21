# EmailTrigger (`*.email_trigger.yaml`)

Routes incoming emails to a script or flow. Each trigger reserves a local-part: emails sent to `<local_part>@<windmill_email_domain>` deliver to the configured runnable. Set `workspaced_local_part: true` to namespace per workspace — recipient becomes `<workspace_id>-<local_part>@…`.

Senders can append URL-style extras with `+`: `mytrigger+foo=bar+baz=qux@…`. These flow through as `email_extra_args`.

## Schema

```yaml
type: object
properties:
  script_path:
    type: string
  permissioned_as:
    type: string
  is_flow:
    type: boolean
  labels:
    type: array
    items:
      type: string
  local_part:
    type: string
  workspaced_local_part:
    type: boolean
required:
- script_path
- permissioned_as
- is_flow
- local_part
```

Plus shared `error_handler_*` and `retry` — see `common-retry.md`.

## Payload

The runnable receives:

- `parsed_email` — `{ headers, text_body, html_body, attachments[] }`. Each `attachment` has `{ headers, body }`.
- `raw_email` — the raw RFC 822 message as a string, **or** an S3 object (`{ s3: "windmill_emails/<job_id>/raw.eml" }`) if the message exceeds 1 MiB.
- `email_extra_args` (optional, only when sender appended `+key=value` extras) — a flat object of the parsed extras.

With a preprocessor, all of the above are nested under `event` along with `event.kind = "email"` and `event.trigger_path` (the trigger's path). Without a preprocessor, `trigger_path` is **not** exposed — add a preprocessor if you need it.

## Attachments are S3 objects

Binary attachments are uploaded to the workspace S3 bucket and surface in `parsed_email.attachments[i].body` as:

```json
{ "s3": "windmill_emails/<job_id>/attachments/<filename>" }
```

Read the bytes with the wmill SDK:

```ts
// TypeScript
import * as wmill from "windmill-client"
const file = await wmill.loadS3File(parsed_email.attachments[0].body)
```

```python
# Python
import wmill
data = wmill.load_s3_file(parsed_email["attachments"][0]["body"])
```

If the workspace has no S3 resource configured (Workspace Settings → Object storage), `body` falls back to the string `"configure s3 in the workspace settings to handle attachments"`. Same for large `raw_email` bodies. Email attachment storage requires the server to be built with the `parquet` feature.

Text/HTML/inline parts appear inline in `body` as strings.
