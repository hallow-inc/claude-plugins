# Raw app load performance

Read before writing a raw app's `backend/` runnables or its load sequence in `App.tsx`. The checkable subset is enforced pre-push as build-policy APP.3–APP.7.

## The cost model

Every `backend.<id>()` call from the browser becomes a **queued job**. A worker on the runnable's tag must pull it before anything runs, and the browser polls for the result. One call costs:

| component | typical | worst seen |
|---|---|---|
| queue wait for a slot | ~30 ms | minutes at peak on `fargate` |
| result polling | up to 500 ms after the job finishes | — |
| DuckLake `connect()` (fresh ATTACH every job) | ~3.9 s | — |
| external API round-trips | per call, serial unless you parallelise | 30 min when unbounded |

The pools are small and shared with crons, flows, and pipelines:

- `default` group (tags `bun`, `python3`, …): **3 slots** on one EC2 host.
- `fargate`: **2–10 Fargate tasks, 1 slot each**, autoscaled with a 60 s scale-out cooldown. Shared with every DuckLake pipeline, Finance cash application, and Batch submit.

On live Windmill (1.735), app jobs carry no priority and pull **behind** queued flow steps on the same tag. Measured 2026-10-05: B2B app jobs on `fargate` waited p90 17 s and p99 196 s, against a p50 execution of 2.8 s. **The queue, not the code, sets the tail.** Every design choice below either removes a job from the load path or keeps it off the contended pool.

## Designing the load path

The **load path** is every backend call a page makes before the user sees useful content. Work through these in order.

### 1. Minimise load-path calls, and fire them in parallel

Target **one or two calls per view**. Collapse related reads into a single runnable that returns everything the view needs; one job pays one queue wait and one `connect()`. Calls that do not depend on each other go out together:

```typescript
const [deal, demo] = await Promise.all([
  backend.get_deal_context({ deal_id }),
  backend.get_demo_context({ deal_id }),
]);
```

A dependent call (B needs A's output) costs two queue waits in series. When that happens, return B's input from A instead.

### 2. Choose the pool by capability

Leave a runnable on the default pool (no tag) unless it needs what only `fargate` provides: the sandbox S3 bucket, the DuckLake catalog, or Batch. A runnable that only calls HubSpot, Slack, Google, or a datatable runs on `default`. A runnable that needs DuckLake for one field and an API for the rest becomes two runnables, so only the DuckLake read waits on `fargate`.

### 3. Make any runnable with settings a path script

A runnable that needs a `tag`, a `timeout`, or a `cache_ttl` is a **path script** (`backend/<id>.yaml` with `type: script` and `path: f/<folder>/<name>`), and those settings live in the script's `*.script.yaml`. This is not optional:

- An inline runnable has no `timeout` field at all (`RawCode`, fork `windmill-types/src/jobs.rs`), so it inherits the 1800 s global default.
- `wmill app push` rebuilds every inline runnable's `inlineScript` from only `content`, `language`, and `lock` (CLI 1.799.0, `raw_apps.ts`). Any `tag` or `cache_ttl` written under `inlineScript` in YAML is silently dropped on push.

Keep inline runnables for fast pure computation and datatable queries.

### 4. Bound every external call in code

A hung call holds a worker slot until the job timeout. Inline runnables have only the 1800 s default, so the in-code bound is their only protection. Bound every `fetch` and SDK call:

```typescript
const res = await fetch(url, { headers, signal: AbortSignal.timeout(10_000) });
```

```typescript
const s3 = new S3Client({
  region,
  maxAttempts: 2,
  requestHandler: { requestTimeout: 10_000, connectionTimeout: 3_000 },
});
```

Set the path script's `timeout` per build-policy SCRIPT.1. App reads rarely need more than 30–60 s.

### 5. Keep each runnable self-contained

A load-path runnable returns its own data; it never waits on another job (`run_wait_result`, blocking `runScript` / `run_script`, `runFlow`). A waiting job holds its slot through the child's queue wait. Quill's demo context waited synchronously on a `fargate` read and inherited every `fargate` queue spike: 13.8 s, 44.7 s. Expose the second read as its own runnable and call it in parallel from the frontend.

### 6. Cache staleness-tolerant reads

Set `cache_ttl` (seconds) on a path script whose data may be minutes old: dashboards, rollups, reference lists. The cache key is **script hash + args**. It does not include the viewer. A runnable whose result depends on who is calling, where that identity is not an argument (`whoami`, row-level filtering by viewer), leaks one viewer's result to the next when cached. Leave those uncached, or pass the identity as an argument.

Two limits on live: a cache hit is checked on the worker, so it still waits for a slot (it skips execution, not the queue). And only successful runs are cached, so a failing read is re-run on every load.

### 7. Render from the last good result, then revalidate

Show content before the first backend call returns. Write each load-path result to `localStorage` and render it immediately on the next open, then replace it when the fresh call returns:

```typescript
const key = `wm:f/b2b/quill/quill:deal:${deal_id}`;
const cached = (() => { try { return JSON.parse(localStorage.getItem(key) ?? 'null') } catch { return null } })();
if (cached) setDeal(cached.value);
const fresh = await backend.get_deal_context({ deal_id });
setDeal(fresh);
try { localStorage.setItem(key, JSON.stringify({ at: Date.now(), value: fresh })) } catch {}
```

- On live, a raw app runs in an unsandboxed iframe loaded from a `blob:` URL, so it shares **one origin with the whole Windmill UI and every other app**. Prefix every key with the app path. Store only data any Hallow Windmill user may see; pricing terms, customer PII, or secrets stay server-side.
- Mark cached content as cached ("updated 3 min ago") until the fresh result lands.
- Wrap every storage access in `try`. Storage can be empty or throw.

### 8. Make interaction cheap

- **Debounce** recomputes driven by typing (Quill's `evaluate` waits 400 ms).
- Hold an **in-flight guard** per call. A re-click while a call is pending reuses the pending promise; it does not enqueue a second job.
- Use a **refresh button**, not `setInterval` polling of a backend runnable. Every interval tick is a job on a shared pool, multiplied by every open tab.
- Give every backend call an error state with a **retry** action. On live, the client cancels a job after five failed result polls, which surfaces as a rejected call. Without an error state, that becomes a blank page.

### 9. Keep the bundle lean

The JS/CSS bundle is served without cache headers on live, so every open downloads it again. Prefer the framework plus what the view needs. Large chart, editor, or date libraries are imported only by the views that render them (`import()` on demand).

## Done when

The app's load path is designed when **every** load-path call can be named with its pool, its bound, and its cache decision, and every item below holds:

- [ ] ≤ 2 load-path calls per view; independent calls fire in parallel.
- [ ] Every `tag: fargate` runnable needs S3, DuckLake, or Batch.
- [ ] Every runnable with a tag, timeout, or cache is a path script carrying those settings.
- [ ] Every external call has an in-code timeout.
- [ ] No load-path runnable waits on another job.
- [ ] `cache_ttl` is set on every staleness-tolerant read and absent from every identity-dependent one.
- [ ] The page renders from `localStorage` on repeat opens, with app-path-prefixed keys and no sensitive values.
- [ ] Interactive calls are debounced and in-flight-guarded; no interval polling; every call has error + retry.

## Shelf life

Several costs above are properties of the live image (1.735), and fork change `isolate-interactive-app-execution` targets them: app-job priority, an `app_*` worker pool, push-based results in place of polling, immutable bundle caching, and cache hits served without a worker. When that change ships, revisit §6's "still waits for a slot", §8's five-failure cancel, and §9. The design rules (fewer calls, no waiting on other jobs, bounded I/O, render-then-revalidate) hold regardless.
