# TypeScript testing reference

How `assure-adapter-typescript` turns vitest tests into evidence. Read this with the `assure-testing`
skill; this file covers only what is specific to TypeScript.

## Running tests

`VER-TESTS-PASS` runs, per unit (the directory holding `package.json`), the tests that import a
changed file, directly or through other modules:

```sh
npx vitest related --run --passWithNoTests --sequence.shuffle <changed files>
```

Run the same flags locally before stopping. A test that passes only in one order fails here. A test
file that fails to load (an import or syntax error) counts as an erroring test, not as no tests.

## Seeding fast-check from `ASSURE_SEED`

The adapter sets `ASSURE_SEED` from the `HEAD` commit for coverage, fuzz, mutation, and fail-on-base.
The vitest CLI cannot add a setup file, so the unit's vitest config must load one that reads it:

```ts
// vitest.setup.ts
import fc from "fast-check";

const seed = Number(process.env.ASSURE_SEED);
if (Number.isInteger(seed)) {
  fc.configureGlobal({ ...fc.readConfigureGlobal(), seed });
}
```

```ts
// vitest.config.ts
import { defineConfig } from "vitest/config";

export default defineConfig({ test: { setupFiles: ["./vitest.setup.ts"] } });
```

Spread `fc.readConfigureGlobal()`: `configureGlobal` replaces the global settings rather than merging
them. Without the setup file, reruns on one commit generate different inputs; nothing fails because
of it, but a mutant or a fail-on-base result can change between runs.

## Properties with fast-check

Use `fc.assert` with `fc.property` inside a vitest `test`:

```ts
import fc from "fast-check";
import { expect, test } from "vitest";
import { decode, encode } from "./codec";

test("decode inverts encode", () => {
  fc.assert(
    fc.property(fc.uint8Array(), (bytes) => {
      expect(decode(encode(bytes))).toEqual(bytes);
    }),
  );
});
```

A failing property prints its seed and shrunk counterexample. Keep the counterexample as an
`examples` entry (below), not as a pinned `seed`.

## Fuzz files (`VER-ROBUST-FUZZ`)

A parser, decoder, or any function over untrusted input gets a fuzz file next to its source:
`src/decode.ts` → `src/decode.fuzz.test.ts`. The adapter finds fuzz files only by that name, for
each changed source file, and runs them with `vitest run`.

```ts
import fc from "fast-check";
import { test } from "vitest";
import { decode } from "./decode";

test("decode never throws on arbitrary text", () => {
  fc.assert(
    fc.property(fc.string(), (text) => {
      decode(text);
    }),
    { examples: [["{\"a\":"], ["\u0000"]] },
  );
});
```

- Every crasher you fix becomes an entry in `examples`. Each entry is a tuple, one value per
  arbitrary. Examples run before generated inputs, on every run, so the entry is the regression
  test.
- The file ends in `.test.ts`, so `VER-TESTS-PASS` runs it too.

## Lint packs

The adapter lints with its own ESLint config, not the unit's, so a rule disabled in the repo config
still reports. Inline `eslint-disable` comments are ignored.

| Objective | Reports |
|---|---|
| `CODE-CHECK-RETURNS` | floating and misused promises |
| `CODE-COMPLEXITY` | each function's cyclomatic complexity, in source files only |
| `CODE-NO-UNSAFE` | `eval`, `new Function`, implied eval, `any`, `@ts-` comments, `as unknown as` (`double-cast`), `Reflect` (`reflect`), `new Proxy` (`proxy`) |
| `CODE-RESOURCE-BOUNDS` | constant loop conditions, and `fetch` without a `signal` (`fetch-signal`) |

`fetch-signal` checks `fetch`, `globalThis.fetch`, `window.fetch`, and `self.fetch` calls whose second
argument is missing or is an object literal with no `signal` property. An init object passed as a
variable is not checked: `fetch(url, init)` passes even when `init` has no signal. Write the init
inline, or pass `AbortSignal.timeout(ms)` as its `signal`.

## Mutation (`VER-MUTATION-CHANGED`)

Stryker, with the vitest runner, mutates only the lines changed since `--changed-from` in tracked
source files. The score is `(Killed + Timeout) / (Killed + Timeout + Survived + NoCoverage)`, against
the level's threshold; `CompileError`, `RuntimeError`, and `Ignored` mutants do not count.

`git add` a new file to have it mutated locally; untracked files are skipped.

## Fail-on-base (`VER-FAIL-ON-BASE`)

For a change with a commit whose subject is typed `fix` (`fix:` or `fix(<scope>):`), the adapter
extracts the repository at the base commit, copies in the change's test files, and runs the tests
whose name is new: a test is selected when its file and full name (describe blocks included) do
not exist at the base. Each must fail there. Follow the fail-first loop in `assure-testing`, with
these TypeScript rules:

- Give the regression test a new name. An edited test keeps its old name and is not selected.
- Reproduce the bug through the exports that existed at the base. A test that imports something the
  fix added fails to load there, and counts as an error, not as a failure.
- Pair every fix with a reproducing test under a new name. A fix that selects no test passes with a
  note, unless the manifest sets `require_fix_test: true`.
