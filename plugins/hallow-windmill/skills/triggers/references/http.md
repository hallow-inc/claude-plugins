# HttpTrigger (`*.http_trigger.yaml`)

Routes inbound HTTP requests at a custom URL to a script or flow. Webhooks are HTTP triggers with `authentication_method: none`.

```yaml
type: object
properties:
  script_path:
    type: string
    description: Path to the script or flow to execute when triggered
  permissioned_as:
    type: string
    description: The user or group this trigger runs as (permissioned_as)
  is_flow:
    type: boolean
    description: True if script_path points to a flow, false if it points to a script
  labels:
    type: array
    items:
      type: string
  route_path:
    type: string
    description: The URL route path that will trigger this endpoint (e.g., 'api/myendpoint').
      Must NOT start with a /.
  static_asset_config:
    type: object
    properties:
      s3:
        type: string
      storage:
        type: string
      filename:
        type: string
    description: Configuration for serving static assets (s3 bucket, storage path, filename)
  http_method:
    type: string
    enum:
    - get
    - post
    - put
    - delete
    - patch
  authentication_resource_path:
    type: string
    description: Path to the resource containing authentication configuration (for
      api_key, basic_http, custom_script, signature methods)
  summary:
    type: string
  description:
    type: string
  request_type:
    type: string
    enum:
    - sync
    - async
    - sync_sse
  authentication_method:
    type: string
    enum:
    - none
    - windmill
    - api_key
    - basic_http
    - custom_script
    - signature
  is_static_website:
    type: boolean
  workspaced_route:
    type: boolean
  wrap_body:
    type: boolean
    description: If true, wraps the request body in a 'body' parameter
  raw_string:
    type: boolean
    description: If true, passes the request body as a raw string instead of parsing as JSON
required:
- script_path
- permissioned_as
- is_flow
- route_path
- request_type
- authentication_method
- http_method
- is_static_website
- workspaced_route
- wrap_body
- raw_string
```

Plus shared `error_handler_*` and `retry` — see `common-retry.md`.

## Auth notes

- `none` → public webhook. Only use with `f/shared/assert_principal` or HMAC signature check inside the script.
- `windmill` → caller must pass a Windmill API token.
- `api_key` / `basic_http` / `signature` → resource at `authentication_resource_path` holds the secret.
- `custom_script` → script at the resource path validates the request, returns truthy/falsey.

## Hallow

`permissioned_as` defaults to the creator. To run as an admin (needed when the trigger calls scripts that touch admin-only resources), set `permissioned_as: u/<admin-user>` and ensure the admin has acl on the trigger.
