import Assure.Glob

namespace Assure

inductive Status where
  | pass
  | waived
  | advisoryFail
  | fail
  deriving Repr, DecidableEq

def Status.rank : Status → Nat
  | .pass => 0
  | .waived => 1
  | .advisoryFail => 2
  | .fail => 3

instance : LE Status := ⟨fun a b => a.rank ≤ b.rank⟩

instance (a b : Status) : Decidable (a ≤ b) := inferInstanceAs (Decidable (a.rank ≤ b.rank))

def Status.name : Status → String
  | .pass => "pass"
  | .waived => "waived"
  | .advisoryFail => "advisory-fail"
  | .fail => "fail"

inductive Level where
  | A | B | C | D
  deriving Repr, DecidableEq

def Level.all : List Level := [.A, .B, .C, .D]

inductive Need where
  | required
  | advisory
  deriving Repr, DecidableEq

inductive AppliesTo where
  | all | formal | inputs | fix
  deriving Repr, DecidableEq

structure Objective where
  id : String
  levels : Level → Option Need
  threshold : Level → Option Nat
  appliesTo : AppliesTo

inductive Role where
  | source
  | other
  deriving Repr, DecidableEq

structure ChangedFile where
  path : String
  level : Level
  role : Role
  formal : Bool
  inputs : Bool
  fix : Bool
  lines : List Nat
  deriving Repr

structure BaseRun where
  failed : Nat
  passed : Nat
  skipped : Nat
  errored : Nat
  requireTest : Bool
  deriving Repr

inductive MutantClass where
  | detected
  | undetected
  | pending
  | excluded
  deriving Repr, DecidableEq

structure Mutant where
  path : String
  level : Level
  cls : MutantClass
  deriving Repr

structure FuzzSuite where
  sources : List String
  cases : List Bool
  deriving Repr

structure CoverFile where
  path : String
  hits : List (Nat × Nat)
  deriving Repr

structure Waiver where
  objective : String
  scope : String
  expires : Nat
  deriving Repr

structure Case where
  objective : Objective
  changed : List ChangedFile
  problems : Nat
  failing : Nat
  base : Option BaseRun
  mutation : Bool
  mutants : List Mutant
  fuzz : Option (List FuzzSuite)
  coverage : Option (List CoverFile)
  waivers : List Waiver
  date : Nat

def inScope (o : Objective) (f : ChangedFile) : Bool :=
  match o.appliesTo with
  | .formal => f.formal
  | .inputs => f.inputs
  | .fix => f.fix
  | .all => true

def applicable (o : Objective) (changed : List ChangedFile) : List ChangedFile :=
  changed.filter fun f => inScope o f && (o.levels f.level).isSome

def isRequired (o : Objective) (changed : List ChangedFile) : Bool :=
  (applicable o changed).any fun f => o.levels f.level == some .required

def active (c : Case) : List Waiver :=
  c.waivers.filter fun w => w.objective == c.objective.id && w.expires ≥ c.date

def matched (ws : List Waiver) (p : String) : Bool :=
  ws.any fun w => globMatch w.scope p

def covered (ws : List Waiver) (paths : List String) : Bool :=
  !paths.isEmpty && ws.any fun w => paths.all (globMatch w.scope)

def baseFailures : BaseRun → Nat
  | b =>
    if b.failed + b.passed + b.skipped + b.errored == 0 then
      if b.requireTest then 1 else 0
    else b.passed + b.skipped + b.errored

def pendingCount (ms : List Mutant) : Nat :=
  (ms.filter fun m => m.cls == .pending).length

def unlocatedCount (c : Case) : Nat :=
  c.problems + c.failing + (c.base.map baseFailures).getD 0 + pendingCount c.mutants

structure Finding where
  path : String
  level : Level
  deriving Repr

def ruleFiles (o : Objective) (changed : List ChangedFile) : List ChangedFile :=
  changed.filter fun f => f.role == .source && inScope o f && (o.levels f.level).isSome

def fuzzFor (suites : List FuzzSuite) (f : ChangedFile) : List Finding :=
  let here := { path := f.path, level := f.level : Finding }
  let named := suites.filter fun s => s.sources.contains f.path
  let cases := named.flatMap (·.cases)
  if named.isEmpty then [here]
  else (if cases.isEmpty then [here] else []) ++ (cases.filter (!·)).map fun _ => here

def fuzzFindings (o : Objective) (changed : List ChangedFile) (suites : List FuzzSuite) :
    List Finding :=
  (ruleFiles o changed).flatMap (fuzzFor suites)

def recordsFor (files : List CoverFile) (p : String) : List CoverFile :=
  files.filter fun cf => cf.path == p

def uncoveredIn (recs : List CoverFile) (l : Nat) : Bool :=
  recs.any (fun cf => cf.hits.lookup l == some 0) &&
    recs.all fun cf => (cf.hits.lookup l).all (· == 0)

def coverageFor (files : List CoverFile) (f : ChangedFile) : List Finding :=
  let here := { path := f.path, level := f.level : Finding }
  let recs := recordsFor files f.path
  if recs.isEmpty then [here]
  else (f.lines.filter (uncoveredIn recs)).map fun _ => here

def coverageFindings (o : Objective) (changed : List ChangedFile) (files : List CoverFile) :
    List Finding :=
  (ruleFiles o changed).flatMap (coverageFor files)

structure Score where
  detected : Nat
  undetected : Nat

def Score.fails (s : Score) : Option Nat → Bool
  | none => s.detected + s.undetected != 0 && s.undetected > 0
  | some th => s.detected + s.undetected != 0 &&
      decide (100 * s.detected < th * (s.detected + s.undetected))

def scored (o : Objective) (ms : List Mutant) (l : Level) : List Mutant :=
  ms.filter fun m => m.level == l && (o.levels l).isSome &&
    (m.cls == .detected || m.cls == .undetected)

def scoreOf (ms : List Mutant) : Score :=
  { detected := (ms.filter fun m => m.cls == .detected).length
    undetected := (ms.filter fun m => m.cls == .undetected).length }

def keptScore (ws : List Waiver) (ms : List Mutant) : Score :=
  scoreOf (ms.filter fun m => !(m.cls == .undetected && matched ws m.path))

structure Flags where
  blocking : Bool := false
  advisory : Bool := false
  waived : Bool := false

def Flags.status (d : Flags) : Status :=
  if d.blocking then .fail
  else if d.advisory then .advisoryFail
  else if d.waived then .waived
  else .pass

def Flags.join (a b : Flags) : Flags :=
  { blocking := a.blocking || b.blocking
    advisory := a.advisory || b.advisory
    waived := a.waived || b.waived }

def unlocatedFlags (n : Nat) (req cover : Bool) : Flags :=
  if n == 0 then {}
  else if cover then { waived := true }
  else { blocking := req, advisory := true }

def mutationFlags (o : Objective) (ws : List Waiver) (ms : List Mutant) : Flags :=
  Level.all.foldl (init := {}) fun d l =>
    let s := scored o ms l
    let th := o.threshold l
    if s.isEmpty then d
    else if (keptScore ws s).fails th then
      d.join { advisory := true, blocking := o.levels l == some .required }
    else if (scoreOf s).fails th then d.join { waived := true }
    else d

def locatedFlags (o : Objective) (req : Bool) (ws : List Waiver) (fs : List Finding) : Flags :=
  fs.foldl (init := {}) fun d f =>
    if matched ws f.path then d.join { waived := true }
    else d.join { advisory := true, blocking := req && o.levels f.level == some .required }

def evaluate (c : Case) : Status :=
  let o := c.objective
  let app := applicable o c.changed
  if app.isEmpty then .pass
  else
    let req := isRequired o c.changed
    let ws := active c
    let cover := covered ws (app.map (·.path))
    let u := unlocatedFlags (unlocatedCount c) req cover
    let m := if c.mutation && pendingCount c.mutants == 0 then mutationFlags o ws c.mutants else ({} : Flags)
    let fz := match c.fuzz with
      | none => ({} : Flags)
      | some s => locatedFlags o req ws (fuzzFindings o c.changed s)
    let cv := match c.coverage with
      | none => ({} : Flags)
      | some cf => locatedFlags o req ws (coverageFindings o c.changed cf)
    (u.join (m.join (fz.join cv))).status

end Assure
