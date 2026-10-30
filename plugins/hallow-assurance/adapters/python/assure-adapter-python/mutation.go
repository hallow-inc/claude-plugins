package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type lineRange struct{ start, end int }

var hunkHeader = regexp.MustCompile(`(?m)^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,([0-9]+))? @@`)

func hunkRanges(diff []byte) ([]lineRange, error) {
	var out []lineRange
	for _, m := range hunkHeader.FindAllSubmatch(diff, -1) {
		start, err := strconv.Atoi(string(m[1]))
		if err != nil {
			return nil, fmt.Errorf("git diff hunk %q: %w", m[0], err)
		}
		count := 1
		if len(m[2]) > 0 {
			if count, err = strconv.Atoi(string(m[2])); err != nil {
				return nil, fmt.Errorf("git diff hunk %q: %w", m[0], err)
			}
		}
		if count > 0 {
			out = append(out, lineRange{start, start + count - 1})
		}
	}
	return out, nil
}

func inRanges(rs []lineRange, line int) bool {
	return slices.ContainsFunc(rs, func(r lineRange) bool { return line >= r.start && line <= r.end })
}

var (
	arithmeticOps = []string{"Add", "Sub", "Mul", "Div", "FloorDiv", "Mod"}
	bitwiseOps    = []string{"RShift", "LShift", "BitOr", "BitAnd", "BitXor"}
	orderingOps   = []string{"Lt", "LtE", "Gt", "GtE"}
	identityOps   = []string{"Is", "IsNot"}
)

// excludedOperators is the D8 operator set expressed as cosmic-ray's exclude list: binary
// replacements leave their family (or involve Pow), ordering and identity comparisons do not swap,
// and exception, decorator and the two random-valued variable operators are dropped.
func excludedOperators() []string {
	family := map[string]int{"Pow": 0}
	for _, o := range arithmeticOps {
		family[o] = 1
	}
	for _, o := range bitwiseOps {
		family[o] = 2
	}
	binary := slices.Concat(arithmeticOps, []string{"Pow"}, bitwiseOps)
	var out []string
	for _, a := range binary {
		for _, b := range binary {
			if a != b && (family[a] != family[b] || family[a] == 0) {
				out = append(out, "core/ReplaceBinaryOperator_"+a+"_"+b+"$")
			}
		}
	}
	for _, a := range orderingOps {
		for _, b := range identityOps {
			out = append(out, "core/ReplaceComparisonOperator_"+a+"_"+b+"$", "core/ReplaceComparisonOperator_"+b+"_"+a+"$")
		}
	}
	for _, o := range []string{"ExceptionReplacer", "RemoveDecorator", "VariableReplacer", "VariableInserter"} {
		out = append(out, "core/"+o+"$")
	}
	return out
}

func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func cosmicConfig(module, testCommand string, timeout time.Duration, ranges []lineRange) string {
	var lines []string
	for _, r := range ranges {
		lines = append(lines, tomlString(fmt.Sprintf("%d-%d", r.start, r.end)))
	}
	var excl []string
	for _, e := range excludedOperators() {
		excl = append(excl, tomlString(e))
	}
	return fmt.Sprintf(`[cosmic-ray]
module-path = %s
timeout = %.1f
excluded-modules = []
test-command = %s

[cosmic-ray.distributor]
name = "local"

[cosmic-ray.filters.operators-filter]
exclude-operators = [%s]

[cosmic-ray.filters.line-filter]
lines = { %s = [%s] }
`, tomlString(module), timeout.Seconds(), tomlString(testCommand), strings.Join(excl, ", "), tomlString(module), strings.Join(lines, ", "))
}

const exitSentinel = "ASSURE_PYTEST_EXIT="

// testScript runs pytest and prints its exit code, because cosmic-ray keeps only stdout and scores
// any non-zero exit as killed, a pytest usage error included.
func testScript(python, seed, argsFile string) string {
	args := []string{shellQuote(python), "-m", "pytest", "-x", "-q", "-p", "no:cacheprovider"}
	for _, a := range seedArgs(seed) {
		args = append(args, shellQuote(a))
	}
	if argsFile != "" {
		args = append(args, shellQuote("@"+argsFile))
	}
	return strings.Join(args, " ") + " 2>&1\nrc=$?\necho " + exitSentinel + "$rc\nexit $rc\n"
}

type cosmicMutation struct {
	ModulePath   string `json:"module_path"`
	OperatorName string `json:"operator_name"`
	StartPos     [2]int `json:"start_pos"`
	EndPos       [2]int `json:"end_pos"`
}

type cosmicResult struct {
	WorkerOutcome string  `json:"worker_outcome"`
	Output        string  `json:"output"`
	TestOutcome   *string `json:"test_outcome"`
	Diff          *string `json:"diff"`
}

type cosmicItem struct {
	Mutations []cosmicMutation `json:"mutations"`
}

var sentinelLine = regexp.MustCompile(`(?m)^` + exitSentinel + `([0-9]+)\s*$`)

// mutantStatus maps a finished cosmic-ray job to a Stryker status from pytest's own exit code.
func mutantStatus(r cosmicResult) (string, error) {
	if r.WorkerOutcome != "normal" {
		return "", fmt.Errorf("cosmic-ray worker outcome %q: %s", r.WorkerOutcome, adapterkit.CapText([]byte(r.Output)))
	}
	if r.TestOutcome != nil && *r.TestOutcome == "killed" && r.Output == "timeout" {
		return "Timeout", nil
	}
	m := sentinelLine.FindAllStringSubmatch(r.Output, -1)
	if len(m) == 0 {
		return "", fmt.Errorf("mutant test run printed no %s line: %s", exitSentinel, adapterkit.CapText([]byte(r.Output)))
	}
	switch m[len(m)-1][1] {
	case "0":
		return "Survived", nil
	case "1", "2":
		return "Killed", nil
	}
	return "", fmt.Errorf("pytest exited %s on a mutant: %s", m[len(m)-1][1], adapterkit.CapText([]byte(r.Output)))
}

var mutationHunk = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+[0-9]+(?:,[0-9]+)? @@`)

// applyDiff rebuilds the mutated file from cosmic-ray's unified diff of it. cosmic-ray diffs the
// file split on "\n", so a trailing newline is one more, empty, line of the diff.
func applyDiff(orig, diff string) (string, error) {
	src := strings.Split(orig, "\n")
	var out []string
	next := 0
	lines := strings.Split(diff, "\n")
	for i := 0; i < len(lines); i++ {
		m := mutationHunk.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		start, _ := strconv.Atoi(m[1])
		if m[2] == "0" {
			start++
		}
		if start-1 < next || start-1 > len(src) {
			return "", fmt.Errorf("mutation diff hunk %q out of order", lines[i])
		}
		out = append(out, src[next:start-1]...)
		next = start - 1
		for i+1 < len(lines) && len(lines[i+1]) > 0 && strings.ContainsRune(" -+", rune(lines[i+1][0])) {
			i++
			l := lines[i]
			if l[0] == '+' {
				out = append(out, l[1:])
				continue
			}
			if next >= len(src) || src[next] != l[1:] {
				return "", fmt.Errorf("mutation diff line %q does not match the source", l)
			}
			if l[0] == ' ' {
				out = append(out, src[next])
			}
			next++
		}
	}
	return strings.Join(append(out, src[next:]...), "\n"), nil
}

func offset(src string, pos [2]int) (int, error) {
	off := 0
	for range pos[0] - 1 {
		i := strings.IndexByte(src[off:], '\n')
		if i < 0 {
			return 0, fmt.Errorf("line %d is past the end of the file", pos[0])
		}
		off += i + 1
	}
	for range pos[1] {
		if off >= len(src) || src[off] == '\n' {
			return 0, fmt.Errorf("column %d is past the end of line %d", pos[1], pos[0])
		}
		_, n := utf8.DecodeRuneInString(src[off:])
		off += n
	}
	return off, nil
}

func replacementText(orig, mutated string, start, end [2]int) (string, error) {
	so, err := offset(orig, start)
	if err != nil {
		return "", err
	}
	eo, err := offset(orig, end)
	if err != nil {
		return "", err
	}
	tail := len(orig) - eo
	if eo < so || len(mutated) < so+tail || mutated[:so] != orig[:so] || mutated[len(mutated)-tail:] != orig[eo:] {
		return "", errors.New("mutation diff changes text outside the mutant's span")
	}
	return mutated[so : len(mutated)-tail], nil
}

type strykerPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type strykerMutant struct {
	ID          string `json:"id"`
	MutatorName string `json:"mutatorName"`
	Replacement string `json:"replacement,omitempty"`
	Location    struct {
		Start strykerPosition `json:"start"`
		End   strykerPosition `json:"end"`
	} `json:"location"`
	Status string `json:"status"`
}

type strykerFile struct {
	Language string          `json:"language"`
	Source   string          `json:"source"`
	Mutants  []strykerMutant `json:"mutants"`
}

type strykerReport struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Thresholds    map[string]int          `json:"thresholds"`
	Files         map[string]*strykerFile `json:"files"`
}

func newStrykerReport() strykerReport {
	return strykerReport{SchemaVersion: "1", Thresholds: map[string]int{"high": 80, "low": 60}, Files: map[string]*strykerFile{}}
}

func mutantID(file string, m strykerMutant) string {
	s, e := m.Location.Start, m.Location.End
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d:%d-%d:%d\x00%s\x00%s", file, s.Line, s.Column, e.Line, e.Column, m.MutatorName, m.Replacement))
	return hex.EncodeToString(sum[:])
}

func (r strykerReport) keepScoped(s adapterkit.Scope) {
	for p, f := range r.Files {
		kept := f.Mutants[:0]
		for _, m := range f.Mutants {
			if s.Contains(p, m.Location.Start.Line) {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			delete(r.Files, p)
			continue
		}
		f.Mutants = kept
	}
}

func (r strykerReport) encode() ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	vs, err := schemas.Validate(schemas.MutationReport, data)
	if err != nil {
		return nil, fmt.Errorf("mutation report: %w", err)
	}
	if len(vs) > 0 {
		return nil, fmt.Errorf("mutation report: %s", vs[0])
	}
	return data, nil
}

// spanLines is the mutant's lines; an end at column 0 stops on the line before.
func spanLines(m cosmicMutation) (int, int) {
	end := m.EndPos[0]
	if m.EndPos[1] == 0 && end > m.StartPos[0] {
		end--
	}
	return m.StartPos[0], end
}

func parseDumpLine(line string) (cosmicMutation, *cosmicResult, error) {
	var entry [2]json.RawMessage
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return cosmicMutation{}, nil, fmt.Errorf("cosmic-ray dump: %w", err)
	}
	var item cosmicItem
	if err := json.Unmarshal(entry[0], &item); err != nil || len(item.Mutations) != 1 {
		return cosmicMutation{}, nil, fmt.Errorf("cosmic-ray dump: work item %s", entry[0])
	}
	var res *cosmicResult
	if err := json.Unmarshal(entry[1], &res); err != nil {
		return cosmicMutation{}, nil, fmt.Errorf("cosmic-ray dump: result %s", entry[1])
	}
	return item.Mutations[0], res, nil
}

func within(ranges []lineRange, mu cosmicMutation) bool {
	first, last := spanLines(mu)
	for l := first; l <= last; l++ {
		if !inRanges(ranges, l) {
			return false
		}
	}
	return true
}

// strykerFor converts one finished job; a job without a diff has no replacement text to tell it
// from a twin on the same span, so it is an error rather than a guess.
func strykerFor(file, orig string, mu cosmicMutation, res *cosmicResult, covered bool) (strykerMutant, error) {
	where := fmt.Sprintf("%s at %d:%d of %s", mu.OperatorName, mu.StartPos[0], mu.StartPos[1], file)
	m := strykerMutant{MutatorName: strings.TrimPrefix(mu.OperatorName, "core/"), Status: "NoCoverage"}
	m.Location.Start = strykerPosition{mu.StartPos[0], mu.StartPos[1] + 1}
	m.Location.End = strykerPosition{mu.EndPos[0], mu.EndPos[1] + 1}
	if res == nil || res.Diff == nil {
		return m, fmt.Errorf("cosmic-ray left mutant %s unrun", where)
	}
	if covered {
		st, err := mutantStatus(*res)
		if err != nil {
			return m, fmt.Errorf("%s: %w", where, err)
		}
		m.Status = st
	}
	mutated, err := applyDiff(orig, *res.Diff)
	if err != nil {
		return m, fmt.Errorf("%s: %w", where, err)
	}
	if m.Replacement, err = replacementText(orig, mutated, mu.StartPos, mu.EndPos); err != nil {
		return m, fmt.Errorf("%s: %w", where, err)
	}
	m.ID = mutantID(file, m)
	return m, nil
}

func mutantsFromDump(tg mutationTarget, orig string, dump []byte, covered bool) ([]strykerMutant, error) {
	out := []strykerMutant{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(dump)), "\n") {
		if line == "" {
			continue
		}
		mu, res, err := parseDumpLine(line)
		if err != nil {
			return nil, err
		}
		kept := within(tg.ranges, mu)
		if tg.scoped {
			kept = inRanges(tg.ranges, mu.StartPos[0])
		}
		if (res != nil && res.WorkerOutcome == "skipped") || !kept {
			continue
		}
		m, err := strykerFor(tg.rel, orig, mu, res, covered)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
