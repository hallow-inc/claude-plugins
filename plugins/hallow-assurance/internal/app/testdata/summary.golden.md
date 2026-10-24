## assure evaluate

Commit `7330276f12e1`, 3 changed files since `origin/master` (`7330276f12e1`), catalog v0, date 2026-09-28.

| Objective | Language | Status |
|---|---|---|
| VER-MUTATION-CHANGED | go | pass |
| VER-ROBUST-FUZZ | go | fail |
| VER-COVERAGE-RESOLUTION | — | waived |
| VER-TESTS-PASS | go | fail |
| FM-COMPLETE | — | advisory-fail |

### VER-ROBUST-FUZZ (go): fail

- internal/x00.go: fuzz target FuzzParse failed on its seed corpus
- internal/x01.go: fuzz target FuzzParse failed on its seed corpus
- internal/x02.go: fuzz target FuzzParse failed on its seed corpus
- internal/x03.go: fuzz target FuzzParse failed on its seed corpus
- internal/x04.go: fuzz target FuzzParse failed on its seed corpus
- internal/x05.go: fuzz target FuzzParse failed on its seed corpus
- internal/x06.go: fuzz target FuzzParse failed on its seed corpus
- internal/x07.go: fuzz target FuzzParse failed on its seed corpus
- internal/x08.go: fuzz target FuzzParse failed on its seed corpus
- internal/x09.go: fuzz target FuzzParse failed on its seed corpus
- internal/x10.go: fuzz target FuzzParse failed on its seed corpus
- internal/x11.go: fuzz target FuzzParse failed on its seed corpus
- internal/x12.go: fuzz target FuzzParse failed on its seed corpus
- internal/x13.go: fuzz target FuzzParse failed on its seed corpus
- internal/x14.go: fuzz target FuzzParse failed on its seed corpus
- internal/x15.go: fuzz target FuzzParse failed on its seed corpus
- internal/x16.go: fuzz target FuzzParse failed on its seed corpus
- internal/x17.go: fuzz target FuzzParse failed on its seed corpus
- internal/x18.go: fuzz target FuzzParse failed on its seed corpus
- internal/x19.go: fuzz target FuzzParse failed on its seed corpus
- … 2 more

### VER-COVERAGE-RESOLUTION: waived

- waived: no adapter lists VER-COVERAGE-RESOLUTION

### VER-TESTS-PASS (go): fail

- failing test: example.com/m/p.TestX: boom
  p_test.go:9: boom

### FM-COMPLETE: advisory-fail

- no adapter lists FM-COMPLETE

### Expired waivers

- VER-FAIL-ON-BASE, scope `**`, approver owner, expired 2026-09-01
