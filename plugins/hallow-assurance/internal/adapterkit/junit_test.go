package adapterkit

import (
	"encoding/xml"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func TestMaxOutputIs16KiB(t *testing.T) {
	if MaxOutput != 16<<10 {
		t.Fatalf("MaxOutput = %d, want %d; tool output in evidence is capped at 16 KiB so one runaway test cannot bloat the evidence", MaxOutput, 16<<10)
	}
}

func TestCapTextKeepsABoundedValidPrefixOfTheTrimmedOutput(t *testing.T) {
	check := func(t interface{ Fatalf(string, ...any) }, in string) {
		got, trimmed := CapText([]byte(in)), strings.TrimSpace(in)
		if trimmed == "" {
			if got != "no output" {
				t.Fatalf("CapText(%q) = %q, want \"no output\"; an erroring case with an empty body tells the reader nothing", in, got)
			}
			return
		}
		if len(got) > MaxOutput {
			t.Fatalf("len(CapText) = %d > MaxOutput %d; stderr would bloat evidence", len(got), MaxOutput)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("CapText result (%d bytes, ends %q) is not valid UTF-8; it lands in junit.xml and error strings, where a split rune corrupts the evidence", len(got), got[max(0, len(got)-4):])
		}
		if !strings.HasPrefix(trimmed, got) {
			t.Fatalf("CapText result (%d bytes) is not a prefix of the %d-byte trimmed input; the head of the output must survive unchanged", len(got), len(trimmed))
		}
		if len(trimmed) <= MaxOutput {
			if len(got) != len(trimmed) {
				t.Fatalf("CapText kept %d of %d trimmed bytes under the cap; below the cap nothing is lost", len(got), len(trimmed))
			}
		} else if len(got) < MaxOutput-(utf8.UTFMax-1) {
			t.Fatalf("CapText of %d bytes gave %d bytes, want at least %d; above the cap only the bytes of one split rune may be dropped", len(trimmed), len(got), MaxOutput-(utf8.UTFMax-1))
		}
	}

	check(t, "a"+strings.Repeat("é", MaxOutput/2))

	rapid.Check(t, func(t *rapid.T) {
		var body string
		switch rapid.IntRange(0, 3).Draw(t, "shape") {
		case 0:
			body = asciiSpace().Draw(t, "blank")
		case 1:
			body = rapid.String().Draw(t, "short")
		case 2:
			unit := rapid.SampledFrom([]string{"a", "é", "€", "😀", " ", "\n"}).Draw(t, "unit")
			shift := strings.Repeat("a", rapid.IntRange(0, 3).Draw(t, "shift"))
			n := max(0, (MaxOutput+rapid.IntRange(-6, 6).Draw(t, "delta")-len(shift))/len(unit))
			body = shift + strings.Repeat(unit, n) + rapid.SampledFrom([]string{"", " ", "\n", "x"}).Draw(t, "tail")
		case 3:
			chunk := rapid.StringN(1, 8, -1).Draw(t, "chunk")
			n := rapid.IntRange(MaxOutput/2, MaxOutput*2).Draw(t, "n")
			body = strings.Repeat(chunk, n/len(chunk)+1)
		}
		check(t, asciiSpace().Draw(t, "lead")+body+asciiSpace().Draw(t, "trail"))
	})
}

func TestCapTextCutsAtTheLastRuneStartWithinTheCapForAnyBytes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lo, hi := byte(0), byte(0xff)
		if rapid.Bool().Draw(t, "continuationOnly") {
			lo, hi = 0x80, 0xbf
		}
		chunk := rapid.SliceOfN(rapid.ByteRange(lo, hi), 1, 8).Draw(t, "chunk")
		n := rapid.IntRange(MaxOutput-4, MaxOutput*2).Draw(t, "n")
		in := []byte(strings.Repeat(string(chunk), n/len(chunk)+1))

		got, trimmed := CapText(in), strings.TrimSpace(string(in))
		if len(got) > MaxOutput {
			t.Fatalf("len(CapText) = %d > MaxOutput %d; malformed tool output must not bloat evidence either", len(got), MaxOutput)
		}
		if got == "no output" && !strings.HasPrefix(trimmed, got) {
			got = ""
		}
		if !strings.HasPrefix(trimmed, got) {
			t.Fatalf("CapText result (%d bytes) is not a prefix of the %d-byte trimmed input", len(got), len(trimmed))
		}
		if len(got) == len(trimmed) {
			return
		}
		if len(trimmed) <= MaxOutput {
			t.Fatalf("CapText kept %d of %d trimmed bytes under the cap; below the cap nothing is lost", len(got), len(trimmed))
		}
		for i := len(got) + 1; i <= MaxOutput && i < len(trimmed); i++ {
			if utf8.RuneStart(trimmed[i]) {
				t.Fatalf("CapText cut at %d but byte %d (%#x) also starts a rune within the cap %d; the cut must be the last rune start at or below the cap so only one split rune is lost", len(got), i, trimmed[i], MaxOutput)
			}
		}
		if len(got) > 0 && !utf8.RuneStart(trimmed[len(got)]) {
			t.Fatalf("CapText cut at %d, inside a rune (next byte %#x); a split rune corrupts the junit.xml evidence", len(got), trimmed[len(got)])
		}
	})
}

func xmlSafeText() *rapid.Generator[string] {
	return rapid.StringOfN(rapid.SampledFrom([]rune{'a', 'Z', '0', '.', ' ', '\t', '\n', '\r', '<', '>', '&', '"', '\'', ']', 'é', '€', '😀'}), 0, 12, -1)
}

func drawXMLCase(t *rapid.T) XMLCase {
	c := XMLCase{Name: xmlSafeText().Draw(t, "name"), Classname: xmlSafeText().Draw(t, "classname")}
	text := &XMLText{Message: xmlSafeText().Draw(t, "message"), Body: xmlSafeText().Draw(t, "body")}
	switch rapid.IntRange(0, 3).Draw(t, "outcome") {
	case 1:
		c.Failure = text
	case 2:
		c.Error = text
	case 3:
		c.Skipped = &struct{}{}
	}
	return c
}

func drawXMLFuzzSuites(t *rapid.T, withProperties bool) []XMLFuzzSuite {
	return rapid.SliceOfN(rapid.Custom(func(t *rapid.T) XMLFuzzSuite {
		s := XMLFuzzSuite{Name: xmlSafeText().Draw(t, "suite")}
		if withProperties {
			s.Properties = rapid.SliceOfN(rapid.Custom(func(t *rapid.T) XMLProperty {
				return XMLProperty{Name: xmlSafeText().Draw(t, "pname"), Value: xmlSafeText().Draw(t, "pvalue")}
			}), 0, 3).Draw(t, "props")
		}
		s.Cases = rapid.SliceOfN(rapid.Custom(drawXMLCase), 0, 4).Draw(t, "cases")
		s.Tests = len(s.Cases)
		return s
	}), 0, 3).Draw(t, "suites")
}

func expectedJUnit(suites []XMLFuzzSuite) ev.JUnit {
	var j ev.JUnit
	for _, s := range suites {
		suite := ev.Suite{Name: s.Name}
		for _, p := range s.Properties {
			suite.Properties = append(suite.Properties, ev.Property{Name: p.Name, Value: p.Value})
		}
		for _, c := range s.Cases {
			name := c.Name
			if c.Classname != "" {
				name = c.Classname + "." + c.Name
			}
			j.Cases++
			outcome := ev.Passed
			switch {
			case c.Failure != nil:
				outcome = ev.Failed
				j.Failures++
				j.Failing = append(j.Failing, ev.Failure{Name: name, Message: c.Failure.Message, Text: c.Failure.Body})
			case c.Error != nil:
				outcome = ev.Errored
				j.Errors++
				j.Failing = append(j.Failing, ev.Failure{Name: name, Message: c.Error.Message, Text: c.Error.Body, Error: true})
			case c.Skipped != nil:
				outcome = ev.Skipped
				j.Skipped++
				j.Skips = append(j.Skips, name)
			default:
				j.Passed = append(j.Passed, name)
			}
			suite.Cases = append(suite.Cases, ev.Case{Name: name, Outcome: outcome})
		}
		j.Suites = append(j.Suites, suite)
	}
	return j
}

func TestXMLTypesRoundTripThroughEvidenceJUnitParser(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		fuzz := rapid.Bool().Draw(t, "fuzz")
		suites := drawXMLFuzzSuites(t, fuzz)
		var doc any = XMLFuzzSuites{Suites: suites}
		if !fuzz {
			plain := XMLSuites{}
			for _, s := range suites {
				plain.Suites = append(plain.Suites, XMLSuite{Name: s.Name, Tests: s.Tests, Cases: s.Cases})
			}
			doc = plain
		}
		data, err := xml.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		got, err := ev.ParseJUnit(append([]byte(xml.Header), data...))
		if err != nil {
			t.Fatalf("assure's JUnit reader rejects the shared XML types, so every adapter's evidence would fail: %v\n%s", err, data)
		}
		if want := expectedJUnit(suites); !reflect.DeepEqual(got, want) {
			t.Fatalf("assure read the shared XML types as\n%+v\nwant\n%+v\nthe Go, Python and TypeScript adapters all write these types; a renamed tag drops suites, cases, properties or failure text, or turns a failing test into a pass\n%s", got, want, data)
		}
	})
}
