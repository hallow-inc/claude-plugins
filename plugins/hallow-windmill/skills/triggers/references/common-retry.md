# Trigger common: retry + error_handler

All trigger types share `error_handler_path`, `error_handler_args`, and `retry` (constant or exponential).

```yaml
error_handler_path:
  type: string
  description: Path to a script or flow to run when the triggered job fails
error_handler_args:
  type: object
  description: The arguments to pass to the script or flow
retry:
  type: object
  properties:
    constant:
      type: object
      properties:
        attempts:
          type: integer
        seconds:
          type: integer
    exponential:
      type: object
      properties:
        attempts:
          type: integer
        multiplier:
          type: integer
        seconds:
          type: integer
          minimum: 1
        random_factor:
          type: integer
          minimum: 0
          maximum: 100
    retry_if:
      $ref: '#/components/schemas/RetryIf'
```

Use `constant` for fixed-interval retry. Use `exponential` for backoff under load — `random_factor` jitters to avoid thundering herd. Pick `retry_if` to gate retries on specific error shapes.
