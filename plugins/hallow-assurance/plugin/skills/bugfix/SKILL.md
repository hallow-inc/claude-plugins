---
name: bugfix
description: Fix a bug fail-first — a verifier reproduces it with a failing test, an implementer fixes it.
argument-hint: <issue | failing seed>
disable-model-invocation: true
---

# Bug fix

Input: `$ARGUMENTS`, either an issue reference or a failing seed (an input that makes the code
fail, such as a fuzz crasher).

1. **Understand the bug.** For an issue, read it with `gh issue view <issue>`. For a seed, run the
   code on it and capture the failure. State the expected and the actual behavior.
2. **Reproduce it red.** Delegate to `hallow-assurance:verifier`: give it the expected and actual
   behavior and the input, and ask for a test that fails on the current code for that reason. A
   seed goes into the fuzz corpus as well. The verifier must not change source.
3. **Confirm red.** Run the new test on the unfixed code. It must fail, and the failure must be the
   bug, not a compile error. If it passes, return to step 2 with what you observed.
4. **Fix.** Delegate to `hallow-assurance:implementer`: give it the bug and the failing test's
   name, and ask for the smallest source change that makes the test pass. The implementer must not
   change tests.
5. **Confirm green.** Run `assure check --fast`. Every objective passes, including the new test.
6. **Hand off.** Report the test, the fix, and the check result. Tell the human to commit with the
   trailer `Assure-Kind: fix` in the message's last paragraph, so `VER-FAIL-ON-BASE` checks that
   the test fails on the base commit. Leave the commit to the human.
