import Assure.Model

namespace Assure

theorem Flags.status_join_rank (a b : Flags) :
    (a.join b).status.rank = max a.status.rank b.status.rank := by
  rcases a with ⟨a1, a2, a3⟩
  rcases b with ⟨b1, b2, b3⟩
  cases a1 <;> cases a2 <;> cases a3 <;> cases b1 <;> cases b2 <;> cases b3 <;> rfl

theorem Flags.join_empty (d : Flags) : d.join {} = d := by
  rcases d with ⟨a, b, c⟩
  simp [Flags.join]

theorem foldl_join_rank_le {α : Type} (g : α → Flags) (l : List α) (init : Flags) (k : Nat) :
    (l.foldl (fun d x => d.join (g x)) init).status.rank ≤ k ↔
      init.status.rank ≤ k ∧ ∀ x ∈ l, (g x).status.rank ≤ k := by
  induction l generalizing init with
  | nil => simp
  | cons x xs ih =>
    simp only [List.foldl, ih, Flags.status_join_rank, Nat.max_le, List.mem_cons,
      forall_eq_or_imp]
    constructor
    · rintro ⟨⟨h1, h2⟩, h3⟩; exact ⟨h1, h2, h3⟩
    · rintro ⟨h1, h2, h3⟩; exact ⟨⟨h1, h2⟩, h3⟩

theorem foldl_join_mono {α β : Type} (g : α → Flags) (g' : β → Flags) (l : List α) (l' : List β)
    (init init' : Flags) (hi : init.status.rank ≤ init'.status.rank)
    (hx : ∀ x ∈ l, ∃ y ∈ l', (g x).status.rank ≤ (g' y).status.rank) :
    (l.foldl (fun d x => d.join (g x)) init).status.rank ≤
      (l'.foldl (fun d y => d.join (g' y)) init').status.rank := by
  have hB := (foldl_join_rank_le g' l' init' _).mp (Nat.le_refl _)
  refine (foldl_join_rank_le g l init _).mpr ⟨Nat.le_trans hi hB.1, fun x hm => ?_⟩
  obtain ⟨y, hy, hle⟩ := hx x hm
  exact Nat.le_trans hle (hB.2 y hy)

def locStep (o : Objective) (req : Bool) (ws : List Waiver) (f : Finding) : Flags :=
  if matched ws f.path then { waived := true }
  else { advisory := true, blocking := req && o.levels f.level == some .required }

theorem locatedFlags_eq (o : Objective) (req : Bool) (ws : List Waiver) (fs : List Finding) :
    locatedFlags o req ws fs = fs.foldl (fun d f => d.join (locStep o req ws f)) {} := by
  unfold locatedFlags locStep
  congr 1
  funext d f
  split <;> rfl

def mutStep (o : Objective) (ws : List Waiver) (ms : List Mutant) (l : Level) : Flags :=
  let s := scored o ms l
  let th := o.threshold l
  if s.isEmpty then {}
  else if (keptScore ws s).fails th then
    { advisory := true, blocking := o.levels l == some .required }
  else if (scoreOf s).fails th then { waived := true }
  else {}

theorem mutationFlags_eq (o : Objective) (ws : List Waiver) (ms : List Mutant) :
    mutationFlags o ws ms = Level.all.foldl (fun d l => d.join (mutStep o ws ms l)) {} := by
  unfold mutationFlags mutStep
  congr 1
  funext d l
  simp only
  split
  · rw [Flags.join_empty]
  · split
    · rfl
    · split
      · rfl
      · rw [Flags.join_empty]

/-! ## 5.1 inapplicable is pass -/

theorem inapplicable_is_pass (c : Case)
    (h : ∀ f ∈ c.changed, inScope c.objective f = true → c.objective.levels f.level = none) :
    evaluate c = .pass := by
  have : applicable c.objective c.changed = [] := by
    unfold applicable
    rw [List.filter_eq_nil_iff]
    intro f hf
    cases hs : inScope c.objective f
    · simp
    · simp [h f hf hs]
  simp [evaluate, this]

/-! ## 5.1 expiry -/

theorem evaluate_eq_of_active (c : Case) (ws : List Waiver)
    (h : active { c with waivers := ws } = active c) :
    evaluate { c with waivers := ws } = evaluate c := by
  unfold evaluate
  rw [h]
  rfl

theorem expiry (c : Case) (pre post : List Waiver) (w : Waiver)
    (hw : c.waivers = pre ++ post) (hexp : w.expires < c.date) :
    evaluate { c with waivers := pre ++ w :: post } = evaluate c := by
  apply evaluate_eq_of_active
  have : ¬ (w.expires ≥ c.date) := Nat.not_le.mpr hexp
  simp [active, hw, List.filter_append, this]

/-! ## 5.3 fail-closed -/

theorem fail_closed (c : Case)
    (hreq : isRequired c.objective c.changed = true)
    (hu : unlocatedCount c ≠ 0)
    (hnc : ∀ w ∈ active c, ∃ f ∈ applicable c.objective c.changed,
      globMatch w.scope f.path = false) :
    evaluate c = .fail := by
  have hne : applicable c.objective c.changed ≠ [] := by
    intro h
    simp [isRequired, h] at hreq
  have hcov : covered (active c) ((applicable c.objective c.changed).map (·.path)) = false := by
    simp only [covered, Bool.and_eq_false_iff, Bool.not_eq_false', List.isEmpty_iff,
      List.any_eq_false, List.all_eq_true, List.mem_map]
    right
    intro w hw hall
    obtain ⟨f, hf, hg⟩ := hnc w hw
    have := hall f.path ⟨f, hf, rfl⟩
    rw [hg] at this
    exact Bool.false_ne_true this
  have hnz : (unlocatedCount c == 0) = false := by simpa using hu
  simp [evaluate, hne, hreq, hcov, unlocatedFlags, hnz, Flags.join, Flags.status]

/-! ## 5.4 monotone in waivers -/

theorem length_filter_mono {α : Type} {l : List α} {p q : α → Bool}
    (h : ∀ x ∈ l, p x = true → q x = true) : (l.filter p).length ≤ (l.filter q).length := by
  rw [← List.countP_eq_length_filter, ← List.countP_eq_length_filter]
  exact List.countP_mono_left h

theorem rank_adv (b : Bool) : 2 ≤ ({ advisory := true, blocking := b } : Flags).status.rank := by
  cases b <;> decide

theorem rank_waived : ({ waived := true } : Flags).status.rank = 1 := rfl

theorem rank_empty : ({} : Flags).status.rank = 0 := rfl

theorem matched_mono {ws ws' : List Waiver} (h : ∀ w ∈ ws', w ∈ ws) (p : String) :
    matched ws' p = true → matched ws p = true := by
  simp only [matched, List.any_eq_true]
  rintro ⟨w, hw, hm⟩
  exact ⟨w, h w hw, hm⟩

theorem Score.fails_mono (s t : Score) (th : Option Nat) (hd : s.detected = t.detected)
    (hu : s.undetected ≤ t.undetected) : s.fails th = true → t.fails th = true := by
  rcases s with ⟨d, u⟩
  rcases t with ⟨d', u'⟩
  simp only at hd hu
  subst hd
  cases th with
  | none => simp [Score.fails]; omega
  | some k =>
    simp only [Score.fails, bne_iff_ne, ne_eq, Bool.and_eq_true, decide_eq_true_eq]
    rintro ⟨_, hlt⟩
    exact ⟨by omega, Nat.lt_of_lt_of_le hlt (Nat.mul_le_mul_left k (by omega))⟩

theorem keptScore_detected (ws : List Waiver) (ms : List Mutant) :
    (keptScore ws ms).detected = (scoreOf ms).detected := by
  simp only [keptScore, scoreOf, List.filter_filter]
  congr 1
  apply List.filter_congr
  intro m _
  cases h : m.cls <;> simp

theorem keptScore_undetected_mono {ws ws' : List Waiver} (h : ∀ w ∈ ws', w ∈ ws)
    (ms : List Mutant) : (keptScore ws ms).undetected ≤ (keptScore ws' ms).undetected := by
  simp only [keptScore, scoreOf, List.filter_filter]
  apply length_filter_mono
  intro m _
  have hmm := matched_mono h m.path
  cases h1 : matched ws m.path <;> cases h2 : matched ws' m.path <;> simp_all

theorem keptScore_undetected_le (ws : List Waiver) (ms : List Mutant) :
    (keptScore ws ms).undetected ≤ (scoreOf ms).undetected := by
  simp only [keptScore, scoreOf, List.filter_filter]
  apply length_filter_mono
  intro m _
  simp only [Bool.and_eq_true]
  exact fun h => h.1

theorem mutStep_le (o : Objective) (ws ws' : List Waiver) (ms ms' : List Mutant) (l : Level)
    (he : (scored o ms l).isEmpty = false → (scored o ms' l).isEmpty = false)
    (hk : (keptScore ws (scored o ms l)).fails (o.threshold l) = true →
      (keptScore ws' (scored o ms' l)).fails (o.threshold l) = true)
    (hs : (scoreOf (scored o ms l)).fails (o.threshold l) = true →
      (scoreOf (scored o ms' l)).fails (o.threshold l) = true) :
    (mutStep o ws ms l).status.rank ≤ (mutStep o ws' ms' l).status.rank := by
  have k2 : (keptScore ws' (scored o ms' l)).fails (o.threshold l) = true →
      (scoreOf (scored o ms' l)).fails (o.threshold l) = true :=
    Score.fails_mono _ _ _ (keptScore_detected _ _) (keptScore_undetected_le _ _)
  have ha := rank_adv (o.levels l == some .required)
  unfold mutStep
  simp only
  cases e : (scored o ms l).isEmpty
  · rw [he e]
    simp only [Bool.false_eq_true, ite_false]
    cases k : (keptScore ws (scored o ms l)).fails (o.threshold l)
    · cases k' : (keptScore ws' (scored o ms' l)).fails (o.threshold l)
      · cases s : (scoreOf (scored o ms l)).fails (o.threshold l)
        · simp only [Bool.false_eq_true, ite_false, rank_empty]; omega
        · simp only [hs s, ite_true, Bool.false_eq_true, ite_false, rank_waived]; omega
      · simp only [Bool.false_eq_true, ite_false, ite_true]
        split
        · rw [rank_waived]; omega
        · rw [rank_empty]; omega
    · simp [hk k]
  · simp only [ite_true, rank_empty]; omega

theorem mutStep_mono (o : Objective) {ws ws' : List Waiver} (h : ∀ w ∈ ws', w ∈ ws)
    (ms : List Mutant) (l : Level) :
    (mutStep o ws ms l).status.rank ≤ (mutStep o ws' ms l).status.rank :=
  mutStep_le o ws ws' ms ms l (fun h => h)
    (Score.fails_mono _ _ _ (by simp [keptScore_detected]) (keptScore_undetected_mono h _))
    (fun h => h)

theorem locStep_mono (o : Objective) (req : Bool) {ws ws' : List Waiver}
    (h : ∀ w ∈ ws', w ∈ ws) (f : Finding) :
    (locStep o req ws f).status.rank ≤ (locStep o req ws' f).status.rank := by
  unfold locStep
  cases hm : matched ws f.path
  · have : matched ws' f.path = false := by
      cases h' : matched ws' f.path
      · rfl
      · rw [matched_mono h _ h'] at hm; contradiction
    simp [this]
  · have := rank_adv (req && o.levels f.level == some .required)
    cases matched ws' f.path
    · simp only [Bool.false_eq_true, ite_false, ite_true, rank_waived]; omega
    · exact Nat.le_refl _

theorem locatedFlags_mono (o : Objective) (req : Bool) {ws ws' : List Waiver}
    (h : ∀ w ∈ ws', w ∈ ws) (fs : List Finding) :
    (locatedFlags o req ws fs).status.rank ≤ (locatedFlags o req ws' fs).status.rank := by
  rw [locatedFlags_eq, locatedFlags_eq]
  exact foldl_join_mono _ _ _ _ _ _ (Nat.le_refl _)
    (fun x hx => ⟨x, hx, locStep_mono o req h x⟩)

theorem mutationFlags_mono (o : Objective) {ws ws' : List Waiver}
    (h : ∀ w ∈ ws', w ∈ ws) (ms : List Mutant) :
    (mutationFlags o ws ms).status.rank ≤ (mutationFlags o ws' ms).status.rank := by
  rw [mutationFlags_eq, mutationFlags_eq]
  exact foldl_join_mono _ _ _ _ _ _ (Nat.le_refl _)
    (fun x hx => ⟨x, hx, mutStep_mono o h ms x⟩)

theorem unlocatedFlags_mono (n : Nat) (req : Bool) {cov cov' : Bool}
    (h : cov' = true → cov = true) :
    (unlocatedFlags n req cov).status.rank ≤ (unlocatedFlags n req cov').status.rank := by
  unfold unlocatedFlags
  split
  · exact Nat.le_refl _
  · have := rank_adv req
    cases cov <;> cases cov'
    · exact Nat.le_refl _
    · exact absurd (h rfl) (by decide)
    · simp only [Bool.false_eq_true, ite_false, ite_true, rank_waived]; omega
    · exact Nat.le_refl _

theorem covered_mono {ws ws' : List Waiver} (h : ∀ w ∈ ws', w ∈ ws) (ps : List String) :
    covered ws' ps = true → covered ws ps = true := by
  simp only [covered, Bool.and_eq_true, List.any_eq_true]
  rintro ⟨hne, w, hw, hall⟩
  exact ⟨hne, w, h w hw, hall⟩

theorem active_sub (c : Case) (ws : List Waiver) (h : ∀ w ∈ ws, w ∈ c.waivers) :
    ∀ w ∈ active { c with waivers := ws }, w ∈ active c := by
  intro w hw
  simp only [active, List.mem_filter] at hw ⊢
  exact ⟨h w hw.1, hw.2⟩

theorem rank_join_mono {a a' b b' : Flags} (ha : a.status.rank ≤ a'.status.rank)
    (hb : b.status.rank ≤ b'.status.rank) :
    (a.join b).status.rank ≤ (a'.join b').status.rank := by
  rw [Flags.status_join_rank, Flags.status_join_rank]
  omega

theorem monotone_in_waivers_sub (c : Case) (ws : List Waiver) (h : ∀ w ∈ ws, w ∈ c.waivers) :
    evaluate c ≤ evaluate { c with waivers := ws } := by
  have hs := active_sub c ws h
  show (evaluate c).rank ≤ (evaluate { c with waivers := ws }).rank
  unfold evaluate
  simp only
  split
  · simp [Status.rank]
  · apply rank_join_mono
    · exact unlocatedFlags_mono _ _ (covered_mono hs _)
    apply rank_join_mono
    · split
      · exact mutationFlags_mono _ hs _
      · exact Nat.le_refl _
    apply rank_join_mono
    · cases c.fuzz
      · exact Nat.le_refl _
      · exact locatedFlags_mono _ _ hs _
    · cases c.coverage
      · exact Nat.le_refl _
      · exact locatedFlags_mono _ _ hs _

theorem monotone_in_waivers (c : Case) (pre post : List Waiver) (w : Waiver)
    (hw : c.waivers = pre ++ w :: post) :
    evaluate c ≤ evaluate { c with waivers := pre ++ post } := by
  apply monotone_in_waivers_sub
  intro x hx
  rw [hw]
  simp only [List.mem_append, List.mem_cons] at hx ⊢
  rcases hx with hx | hx
  · exact Or.inl hx
  · exact Or.inr (Or.inr hx)

/-! ## 5.5 monotone in evidence -/

theorem unlocatedFlags_mono_n {n n' : Nat} (req cov : Bool) (h : n ≤ n') :
    (unlocatedFlags n req cov).status.rank ≤ (unlocatedFlags n' req cov).status.rank := by
  unfold unlocatedFlags
  cases e : (n == 0)
  · have : (n' == 0) = false := by simp at e ⊢; omega
    rw [this]
    exact Nat.le_refl _
  · simp only [ite_true, rank_empty]
    exact Nat.zero_le _

theorem monotone_in_unlocated (c : Case) (p f : Nat) (b : Option BaseRun)
    (h : unlocatedCount c ≤ unlocatedCount { c with problems := p, failing := f, base := b }) :
    evaluate c ≤ evaluate { c with problems := p, failing := f, base := b } := by
  show (evaluate c).rank ≤ (evaluate { c with problems := p, failing := f, base := b }).rank
  unfold evaluate
  simp only
  split
  · exact Nat.le_refl _
  · exact rank_join_mono (unlocatedFlags_mono_n _ _ h) (Nat.le_refl _)

theorem monotone_add_problem (c : Case) :
    evaluate c ≤ evaluate { c with problems := c.problems + 1 } :=
  monotone_in_unlocated c _ c.failing c.base (by simp only [unlocatedCount]; omega)

theorem monotone_add_failing_test (c : Case) :
    evaluate c ≤ evaluate { c with failing := c.failing + 1 } :=
  monotone_in_unlocated c c.problems _ c.base (by simp only [unlocatedCount]; omega)

theorem baseFailures_mono (b b' : BaseRun) (hf : b'.failed = b.failed)
    (hr : b'.requireTest = b.requireTest) (hp : b.passed ≤ b'.passed)
    (hs : b.skipped ≤ b'.skipped) (he : b.errored ≤ b'.errored) :
    baseFailures b ≤ baseFailures b' := by
  simp only [baseFailures]
  rw [hf, hr]
  repeat' split
  all_goals first | omega | (simp_all; done) | (simp_all; omega)

theorem monotone_add_base_failure (c : Case) (b b' : BaseRun) (hb : c.base = some b)
    (hf : b'.failed = b.failed) (hr : b'.requireTest = b.requireTest)
    (hp : b.passed ≤ b'.passed) (hs : b.skipped ≤ b'.skipped) (he : b.errored ≤ b'.errored) :
    evaluate c ≤ evaluate { c with base := some b' } := by
  apply monotone_in_unlocated c c.problems c.failing
  have := baseFailures_mono b b' hf hr hp hs he
  simp [unlocatedCount, hb]
  omega

theorem filter_insert_false {α : Type} (pre post : List α) (m : α) (p : α → Bool)
    (h : p m = false) : (pre ++ m :: post).filter p = (pre ++ post).filter p := by
  simp [List.filter_append, h]

theorem filter_insert_le {α : Type} (pre post : List α) (m : α) (p : α → Bool) :
    ((pre ++ post).filter p).length ≤ ((pre ++ m :: post).filter p).length :=
  ((List.sublist_cons_self m post).append_left pre).filter p |>.length_le

theorem monotone_add_undetected_mutant (c : Case) (pre post : List Mutant) (m : Mutant)
    (hm : m.cls = .undetected) (hc : c.mutants = pre ++ post) :
    evaluate c ≤ evaluate { c with mutants := pre ++ m :: post } := by
  have hpend : pendingCount (pre ++ m :: post) = pendingCount c.mutants := by
    simp only [pendingCount, hc]
    rw [filter_insert_false _ _ _ _ (by simp [hm])]
  have hstep : ∀ ws l, (mutStep c.objective ws c.mutants l).status.rank ≤
      (mutStep c.objective ws (pre ++ m :: post) l).status.rank := by
    intro ws l
    rw [hc]
    apply mutStep_le
    · intro e
      simp only [scored, List.isEmpty_eq_false_iff_exists_mem] at e ⊢
      obtain ⟨x, hx⟩ := e
      exact ⟨x, (((List.sublist_cons_self m post).append_left pre).filter _).subset hx⟩
    · apply Score.fails_mono
      · simp only [keptScore, scoreOf, scored, List.filter_filter]
        rw [filter_insert_false _ _ _ _ (by simp [hm])]
      · simp only [keptScore, scoreOf, scored, List.filter_filter]
        exact filter_insert_le _ _ _ _
    · apply Score.fails_mono
      · simp only [scoreOf, scored, List.filter_filter]
        rw [filter_insert_false _ _ _ _ (by simp [hm])]
      · simp only [scoreOf, scored, List.filter_filter]
        exact filter_insert_le _ _ _ _
  show (evaluate c).rank ≤ (evaluate { c with mutants := pre ++ m :: post }).rank
  unfold evaluate
  simp only
  split
  · exact Nat.le_refl _
  · have hu : unlocatedCount { c with mutants := pre ++ m :: post } = unlocatedCount c := by
      simp only [unlocatedCount, hpend]
    rw [hu]
    apply rank_join_mono (Nat.le_refl _)
    apply rank_join_mono _ (Nat.le_refl _)
    rw [hpend]
    split
    · rw [mutationFlags_eq, mutationFlags_eq]
      exact foldl_join_mono _ _ _ _ _ _ (Nat.le_refl _) (fun x hx => ⟨x, hx, hstep _ x⟩)
    · exact Nat.le_refl _

/-! ## 5.2 order independence -/

theorem Status.rank_inj {a b : Status} (h : a.rank = b.rank) : a = b := by
  cases a <;> cases b <;> simp_all [Status.rank]

theorem any_perm {α : Type} {l l' : List α} (h : l.Perm l') (p : α → Bool) :
    l.any p = l'.any p :=
  Bool.eq_iff_iff.mpr (by simp only [List.any_eq_true, h.mem_iff])

theorem all_perm {α : Type} {l l' : List α} (h : l.Perm l') (p : α → Bool) :
    l.all p = l'.all p :=
  Bool.eq_iff_iff.mpr (by simp only [List.all_eq_true, h.mem_iff])

theorem locatedFlags_perm (o : Objective) (req : Bool) (ws : List Waiver)
    {fs fs' : List Finding} (h : fs.Perm fs') :
    (locatedFlags o req ws fs).status.rank = (locatedFlags o req ws fs').status.rank := by
  rw [locatedFlags_eq, locatedFlags_eq]
  apply Nat.le_antisymm
  · exact foldl_join_mono _ _ _ _ _ _ (Nat.le_refl _)
      (fun x hx => ⟨x, h.mem_iff.mp hx, Nat.le_refl _⟩)
  · exact foldl_join_mono _ _ _ _ _ _ (Nat.le_refl _)
      (fun x hx => ⟨x, h.mem_iff.mpr hx, Nat.le_refl _⟩)

theorem order_independent_waivers (c : Case) (ws : List Waiver) (h : ws.Perm c.waivers) :
    evaluate { c with waivers := ws } = evaluate c := by
  apply Status.rank_inj
  apply Nat.le_antisymm
  · have := monotone_in_waivers_sub { c with waivers := ws } c.waivers
      (fun w hw => h.mem_iff.mpr hw)
    exact this
  · exact monotone_in_waivers_sub c ws (fun w hw => h.mem_iff.mp hw)

theorem scoreOf_perm {l l' : List Mutant} (h : l.Perm l') : scoreOf l = scoreOf l' := by
  simp only [scoreOf, (h.filter _).length_eq]

theorem order_independent_mutants (c : Case) (ms : List Mutant) (h : ms.Perm c.mutants) :
    evaluate { c with mutants := ms } = evaluate c := by
  have hp : pendingCount ms = pendingCount c.mutants := by
    simp only [pendingCount, (h.filter _).length_eq]
  have hm : ∀ ws, mutationFlags c.objective ws ms = mutationFlags c.objective ws c.mutants := by
    intro ws
    rw [mutationFlags_eq, mutationFlags_eq]
    congr 1
    funext d l
    have hs : (scored c.objective ms l).Perm (scored c.objective c.mutants l) := h.filter _
    have hk : keptScore ws (scored c.objective ms l) = keptScore ws (scored c.objective c.mutants l) :=
      scoreOf_perm (hs.filter _)
    simp only [mutStep, hk, scoreOf_perm hs, hs.isEmpty_eq]
  simp only [evaluate, unlocatedCount, hp, hm, active]

theorem fuzzFindings_perm (o : Objective) (changed : List ChangedFile)
    {s s' : List FuzzSuite} (h : s.Perm s') :
    fuzzFindings o changed s = fuzzFindings o changed s' := by
  unfold fuzzFindings
  congr 1
  funext f
  have hn := h.filter (fun x => x.sources.contains f.path)
  have hc := hn.flatMap_right (·.cases)
  simp only [fuzzFor, List.map_const', hn.isEmpty_eq, hc.isEmpty_eq, (hc.filter _).length_eq]

theorem order_independent_fuzz (c : Case) (s s' : List FuzzSuite) (hs : c.fuzz = some s)
    (h : s'.Perm s) : evaluate { c with fuzz := some s' } = evaluate c := by
  cases c; subst hs
  simp only [evaluate, active, unlocatedCount, fuzzFindings_perm _ _ h]

theorem uncoveredIn_perm {r r' : List CoverFile} (h : r.Perm r') :
    uncoveredIn r = uncoveredIn r' := by
  funext l
  simp only [uncoveredIn, any_perm h, all_perm h]

theorem coverageFindings_perm (o : Objective) (changed : List ChangedFile)
    {fs fs' : List CoverFile} (h : fs.Perm fs') :
    coverageFindings o changed fs = coverageFindings o changed fs' := by
  unfold coverageFindings
  congr 1
  funext f
  have hr : (recordsFor fs f.path).Perm (recordsFor fs' f.path) := h.filter _
  simp only [coverageFor, hr.isEmpty_eq, uncoveredIn_perm hr]

theorem order_independent_coverage (c : Case) (fs fs' : List CoverFile)
    (hc : c.coverage = some fs) (h : fs'.Perm fs) :
    evaluate { c with coverage := some fs' } = evaluate c := by
  cases c; subst hc
  simp only [evaluate, active, unlocatedCount, coverageFindings_perm _ _ h]

theorem order_independent_changed (c : Case) (ch : List ChangedFile) (h : ch.Perm c.changed) :
    evaluate { c with changed := ch } = evaluate c := by
  apply Status.rank_inj
  have ha : (applicable c.objective ch).Perm (applicable c.objective c.changed) := h.filter _
  have hr : (ruleFiles c.objective ch).Perm (ruleFiles c.objective c.changed) := h.filter _
  have hreq : isRequired c.objective ch = isRequired c.objective c.changed := by
    simp only [isRequired, any_perm ha]
  have hcov : ∀ ws, covered ws ((applicable c.objective ch).map (·.path)) =
      covered ws ((applicable c.objective c.changed).map (·.path)) := by
    intro ws
    have hp := ha.map (·.path)
    simp only [covered, hp.isEmpty_eq, all_perm hp]
  have hfz : ∀ s, (fuzzFindings c.objective ch s).Perm (fuzzFindings c.objective c.changed s) :=
    fun s => hr.flatMap_right _
  have hcv : ∀ fs, (coverageFindings c.objective ch fs).Perm
      (coverageFindings c.objective c.changed fs) := fun fs => hr.flatMap_right _
  unfold evaluate
  simp only [ha.isEmpty_eq, hreq, hcov, active, unlocatedCount]
  split
  · rfl
  · rw [Flags.status_join_rank, Flags.status_join_rank, Flags.status_join_rank,
      Flags.status_join_rank, Flags.status_join_rank, Flags.status_join_rank]
    rcases c.fuzz with _ | s <;> rcases c.coverage with _ | fs <;>
      simp only [locatedFlags_perm _ _ _ (hfz _), locatedFlags_perm _ _ _ (hcv _)]

/-! ## 5.5 monotone in evidence: fuzz cases and uncovered lines -/

theorem locatedFlags_mono_findings (o : Objective) (req : Bool) (ws : List Waiver)
    {fs fs' : List Finding} (h : ∀ x ∈ fs, x ∈ fs') :
    (locatedFlags o req ws fs).status.rank ≤ (locatedFlags o req ws fs').status.rank := by
  rw [locatedFlags_eq, locatedFlags_eq]
  exact foldl_join_mono _ _ _ _ _ _ (Nat.le_refl _) (fun x hx => ⟨x, h x hx, Nat.le_refl _⟩)

theorem monotone_in_fuzz (c : Case) (s s' : List FuzzSuite) (hs : c.fuzz = some s)
    (h : ∀ f, ∀ x ∈ fuzzFor s f, x ∈ fuzzFor s' f) :
    evaluate c ≤ evaluate { c with fuzz := some s' } := by
  cases c
  subst hs
  show Status.rank _ ≤ Status.rank _
  simp only [evaluate, active, unlocatedCount]
  split
  · exact Nat.le_refl _
  · refine rank_join_mono (Nat.le_refl _) (rank_join_mono (Nat.le_refl _)
      (rank_join_mono (locatedFlags_mono_findings _ _ _ ?_) (Nat.le_refl _)))
    intro x hx
    simp only [fuzzFindings, List.mem_flatMap] at hx ⊢
    obtain ⟨f, hf, hx⟩ := hx
    exact ⟨f, hf, h f x hx⟩

theorem monotone_in_coverage (c : Case) (fs fs' : List CoverFile) (hc : c.coverage = some fs)
    (h : ∀ f, ∀ x ∈ coverageFor fs f, x ∈ coverageFor fs' f) :
    evaluate c ≤ evaluate { c with coverage := some fs' } := by
  cases c
  subst hc
  show Status.rank _ ≤ Status.rank _
  simp only [evaluate, active, unlocatedCount]
  split
  · exact Nat.le_refl _
  · refine rank_join_mono (Nat.le_refl _) (rank_join_mono (Nat.le_refl _)
      (rank_join_mono (Nat.le_refl _) (locatedFlags_mono_findings _ _ _ ?_)))
    intro x hx
    simp only [coverageFindings, List.mem_flatMap] at hx ⊢
    obtain ⟨f, hf, hx⟩ := hx
    exact ⟨f, hf, h f x hx⟩

theorem fuzzFor_here (s : List FuzzSuite) (f : ChangedFile) (x : Finding)
    (hx : x ∈ fuzzFor s f) : x = { path := f.path, level := f.level } := by
  simp only [fuzzFor] at hx
  split at hx
  · simpa using hx
  · simp only [List.mem_append, List.mem_map] at hx
    rcases hx with hx | ⟨_, _, hx⟩
    · split at hx <;> simp_all
    · exact hx.symm

theorem monotone_add_failing_fuzz_case (c : Case) (pre post : List FuzzSuite) (s : FuzzSuite)
    (hs : c.fuzz = some (pre ++ s :: post)) :
    evaluate c ≤ evaluate { c with fuzz := some (pre ++ { s with cases := false :: s.cases } :: post) } := by
  apply monotone_in_fuzz c _ _ hs
  intro f x hx
  have hxe := fuzzFor_here _ f x hx
  cases hp : s.sources.contains f.path
  · have : fuzzFor (pre ++ { s with cases := false :: s.cases } :: post) f =
        fuzzFor (pre ++ s :: post) f := by
      have hp' : f.path ∉ s.sources := by simpa using hp
      simp [fuzzFor, List.filter_append, hp']
    rw [this]; exact hx
  · subst hxe
    have hp' : f.path ∈ s.sources := by simpa using hp
    simp [fuzzFor, List.filter_append, hp']

theorem coverageFor_here (fs : List CoverFile) (f : ChangedFile) (x : Finding)
    (hx : x ∈ coverageFor fs f) : x = { path := f.path, level := f.level } := by
  simp only [coverageFor] at hx
  split at hx
  · simpa using hx
  · simp only [List.mem_map] at hx
    obtain ⟨_, _, hx⟩ := hx
    exact hx.symm

theorem uncoveredIn_insert (rp rq : List CoverFile) (cf : CoverFile) (n l : Nat)
    (hu : uncoveredIn (rp ++ cf :: rq) l = true) :
    uncoveredIn (rp ++ { cf with hits := (n, 0) :: cf.hits } :: rq) l = true := by
  simp only [uncoveredIn, List.any_append, List.all_append, List.any_cons, List.all_cons,
    Bool.and_eq_true, Bool.or_eq_true, List.lookup] at hu ⊢
  by_cases hl : l = n
  · subst hl
    simp only [beq_self_eq_true]
    simp_all
  · have : (l == n) = false := by simp [hl]
    simp only [this]
    exact hu

theorem monotone_add_uncovered_line (c : Case) (pre post : List CoverFile) (cf : CoverFile)
    (n : Nat) (hc : c.coverage = some (pre ++ cf :: post)) :
    evaluate c ≤
      evaluate { c with coverage := some (pre ++ { cf with hits := (n, 0) :: cf.hits } :: post) } := by
  apply monotone_in_coverage c _ _ hc
  intro f x hx
  by_cases hp : cf.path = f.path
  · have hr : recordsFor (pre ++ cf :: post) f.path =
        recordsFor pre f.path ++ cf :: recordsFor post f.path := by
      simp [recordsFor, List.filter_append, hp]
    have hr' : recordsFor (pre ++ { cf with hits := (n, 0) :: cf.hits } :: post) f.path =
        recordsFor pre f.path ++ { cf with hits := (n, 0) :: cf.hits } :: recordsFor post f.path := by
      simp [recordsFor, List.filter_append, hp]
    simp only [coverageFor, hr, hr', List.isEmpty_iff, List.append_eq_nil_iff, reduceCtorEq,
      and_false, ite_false, List.mem_map, List.mem_filter] at hx ⊢
    obtain ⟨l, ⟨hl, hu⟩, he⟩ := hx
    exact ⟨l, ⟨hl, uncoveredIn_insert _ _ _ _ _ hu⟩, he⟩
  · have : coverageFor (pre ++ { cf with hits := (n, 0) :: cf.hits } :: post) f =
        coverageFor (pre ++ cf :: post) f := by
      simp [coverageFor, recordsFor, List.filter_append, hp]
    rw [this]; exact hx

end Assure

theorem seeded : 1 = 2 := by sorry
