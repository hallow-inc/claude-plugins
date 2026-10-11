## assure evaluate

Commit `7330276f12e1`, 3 changed files since `origin/master` (`7330276f12e1`), catalog v0, date 2026-09-28.

| Objective | Language | Status |
|---|---|---|
| CODE-COMPLEXITY | go | pass |
| CODE-RESOURCE-BOUNDS | go | fail |
| IND-VERIFIER-DISTINCT | — | waived |
| VER-TESTS-PASS | go | fail |
| FM-COMPLETE | — | advisory-fail |

### CODE-RESOURCE-BOUNDS (go): fail

- internal/x00.go: noctx: use CommandContext
- internal/x01.go: noctx: use CommandContext
- internal/x02.go: noctx: use CommandContext
- internal/x03.go: noctx: use CommandContext
- internal/x04.go: noctx: use CommandContext
- internal/x05.go: noctx: use CommandContext
- internal/x06.go: noctx: use CommandContext
- internal/x07.go: noctx: use CommandContext
- internal/x08.go: noctx: use CommandContext
- internal/x09.go: noctx: use CommandContext
- internal/x10.go: noctx: use CommandContext
- internal/x11.go: noctx: use CommandContext
- internal/x12.go: noctx: use CommandContext
- internal/x13.go: noctx: use CommandContext
- internal/x14.go: noctx: use CommandContext
- internal/x15.go: noctx: use CommandContext
- internal/x16.go: noctx: use CommandContext
- internal/x17.go: noctx: use CommandContext
- internal/x18.go: noctx: use CommandContext
- internal/x19.go: noctx: use CommandContext
- … 2 more

### IND-VERIFIER-DISTINCT: waived

- waived: no adapter lists IND-VERIFIER-DISTINCT

### VER-TESTS-PASS (go): fail

- failing test: example.com/m/p.TestX: boom
  p_test.go:9: boom

### FM-COMPLETE: advisory-fail

- no adapter lists FM-COMPLETE

### Expired waivers

- VER-FAIL-ON-BASE, scope `**`, approver owner, expired 2026-09-01

### Removable baseline entries

- CODE-RESOURCE-BOUNDS `noctx` in `internal/app/check.go`: use CommandContext (1 of 2 unused)
