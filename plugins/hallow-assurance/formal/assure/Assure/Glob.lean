namespace Assure

inductive Seg where
  | dstar
  | pat (cs : List Char)
  deriving Repr, DecidableEq

def matchSeg : List Char → List Char → Bool
  | [], s => s.isEmpty
  | '*' :: p, s =>
    matchSeg p s || match s with
      | [] => false
      | _ :: s' => matchSeg ('*' :: p) s'
  | '?' :: p, s => match s with
    | [] => false
    | _ :: s' => matchSeg p s'
  | c :: p, s => match s with
    | [] => false
    | d :: s' => c == d && matchSeg p s'
termination_by p s => p.length + s.length

def matchSegs : List Seg → List String → Bool
  | [], parts => parts.isEmpty
  | .dstar :: rest, parts => anySuffix rest parts
  | .pat p :: rest, parts => match parts with
    | [] => false
    | x :: xs => matchSeg p x.toList && matchSegs rest xs
where
  anySuffix (rest : List Seg) : List String → Bool
    | [] => matchSegs rest []
    | x :: xs => matchSegs rest (x :: xs) || anySuffix rest xs

def compileGlob (src : String) : List Seg :=
  (src.splitOn "/").map fun s => if s == "**" then .dstar else .pat s.toList

def globMatch (scope path : String) : Bool :=
  matchSegs (compileGlob scope) (path.splitOn "/")

def inGlobDomain (scope : String) : Bool :=
  !scope.any (fun c => c == '[' || c == '\\')

end Assure
