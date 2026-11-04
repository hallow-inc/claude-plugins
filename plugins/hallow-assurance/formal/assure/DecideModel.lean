import Lean.Data.Json
import Assure

open Lean (Json)
open Assure

abbrev P := Except String

def field (j : Json) (k : String) : P Json := j.getObjVal? k

def nat (j : Json) (k : String) : P Nat := do
  match (← field j k).getNat? with
  | .ok n => pure n
  | .error _ => throw s!"{k}: not a whole number"

def str (j : Json) (k : String) : P String := do (← field j k).getStr?

def flag (j : Json) (k : String) : P Bool := do (← field j k).getBool?

def arr (j : Json) (k : String) : P (List Json) := do pure (← (← field j k).getArr?).toList

def opt (j : Json) (k : String) (f : Json → P α) : P (Option α) :=
  match j.getObjVal? k with
  | .error _ => pure none
  | .ok .null => pure none
  | .ok v => some <$> f v

def level : String → P Level
  | "A" => pure .A
  | "B" => pure .B
  | "C" => pure .C
  | "D" => pure .D
  | s => throw s!"level {s} is not A, B, C or D"

def need : Json → P Need
  | .str "required" => pure .required
  | .str "advisory" => pure .advisory
  | v => throw s!"level status {v.compress} is not required or advisory"

def wholeNumber (k : String) (v : Json) : P Nat :=
  match v.getNat? with
  | .ok n => pure n
  | .error _ => throw s!"threshold {k}: {v.compress} is not a whole number"

def perLevel (j : Json) (k : String) (f : String → Json → P α) : P (Level → Option α) := do
  let obj ← (← field j k).getObj?
  let mut out : List (Level × α) := []
  for ⟨l, v⟩ in obj.toArray do
    out := (← level l, ← f l v) :: out
  pure fun l => out.lookup l

def appliesTo : String → P AppliesTo
  | "" => pure .all
  | "formal" => pure .formal
  | "inputs" => pure .inputs
  | "fix" => pure .fix
  | s => throw s!"applies_to {s} is not formal, inputs or fix"

def date (s : String) : P Nat := do
  let cs := s.toList
  let digits := [0, 1, 2, 3, 5, 6, 8, 9].map fun i => cs.getD i ' '
  unless cs.length == 10 && cs.getD 4 ' ' == '-' && cs.getD 7 ' ' == '-' && digits.all Char.isDigit do
    throw s!"date {s} is not YYYY-MM-DD"
  pure (digits.foldl (fun n c => 10 * n + (c.toNat - '0'.toNat)) 0)

def objective (j : Json) : P Objective := do
  pure {
    id := ← str j "id"
    levels := ← perLevel j "levels" fun _ v => need v
    threshold := ← perLevel j "threshold" wholeNumber
    appliesTo := ← appliesTo (← str j "applies_to") }

def changedFile (j : Json) : P ChangedFile := do
  pure {
    path := ← str j "path"
    level := ← level (← str j "level")
    role := if (← str j "role") == "source" then .source else .other
    formal := ← flag j "formal"
    inputs := ← flag j "inputs"
    fix := ← flag j "fix"
    lines := ← (← arr j "lines").mapM fun v => v.getNat? }

def baseRun (j : Json) : P BaseRun := do
  pure {
    failed := ← nat j "failed"
    passed := ← nat j "passed"
    skipped := ← nat j "skipped"
    errored := ← nat j "errored"
    requireTest := ← flag j "require_test" }

def mutantClass : String → MutantClass
  | "Killed" | "Timeout" => .detected
  | "Survived" | "NoCoverage" => .undetected
  | "Pending" => .pending
  | _ => .excluded

def mutant (j : Json) : P Mutant := do
  pure {
    path := ← str j "path"
    level := ← level (← str j "level")
    cls := mutantClass (← str j "status") }

def fuzzSuite (j : Json) : P FuzzSuite := do
  pure {
    sources := ← (← arr j "sources").mapM Json.getStr?
    cases := ← (← arr j "cases").mapM fun v => do pure ((← v.getStr?) == "passed") }

def hit (v : Json) : P (Nat × Nat) := do
  match (← v.getArr?).toList with
  | [l, h] => pure (← l.getNat?, ← h.getNat?)
  | _ => throw s!"hit {v.compress} is not [line, hits]"

def coverFile (j : Json) : P CoverFile := do
  pure { path := ← str j "path", hits := ← (← arr j "hits").mapM hit }

def waiver (j : Json) : P Waiver := do
  let scope ← str j "scope"
  unless inGlobDomain scope do throw s!"scope {scope} uses a character class or escape"
  pure { objective := ← str j "objective", scope, expires := ← date (← str j "expires") }

def parseCase (line : String) : P Case := do
  let j ← Json.parse line
  pure {
    objective := ← objective (← field j "objective")
    changed := ← (← arr j "changed").mapM changedFile
    problems := ← nat j "problems"
    failing := ← nat j "failing"
    base := ← opt j "base" baseRun
    mutation := ← flag j "mutation"
    mutants := ← (← arr j "mutants").mapM mutant
    fuzz := ← opt j "fuzz" fun v => do (← arr v "suites").mapM fuzzSuite
    coverage := ← opt j "coverage" fun v => do (← arr v "files").mapM coverFile
    waivers := ← (← arr j "waivers").mapM waiver
    date := ← date (← str j "date") }

partial def loop (stdin : IO.FS.Stream) (stdout : IO.FS.Stream) (ok : Bool) : IO Bool := do
  let line ← stdin.getLine
  if line.isEmpty then return ok
  let line := String.ofList (line.toList.filter fun c => c != '\n' && c != '\r')
  if line.isEmpty then loop stdin stdout ok
  else match parseCase line with
    | .ok c =>
      stdout.putStrLn (evaluate c).name
      loop stdin stdout ok
    | .error e =>
      stdout.putStrLn s!"error: {e}"
      loop stdin stdout false

def main : IO UInt32 := do
  let ok ← loop (← IO.getStdin) (← IO.getStdout) true
  pure (if ok then 0 else 1)
