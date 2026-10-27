package adapterkit

import (
	"math/big"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var versionComponent = rapid.OneOf(
	rapid.StringMatching(`0{0,3}[0-9]{1,3}`),
	rapid.SampledFrom([]string{"0", "1", "2", "9", "10", "12", "13", "99", "100"}),
	rapid.StringMatching(`0{0,2}[1-9][0-9]{18,24}`),
)

func drawVersion(t *rapid.T, label string) string {
	return versionComponent.Draw(t, label+".x") + "." + versionComponent.Draw(t, label+".y") + "." + versionComponent.Draw(t, label+".z")
}

func numericOrder(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, _ := new(big.Int).SetString(as[i], 10)
		y, _ := new(big.Int).SetString(bs[i], 10)
		if c := x.Cmp(y); c != 0 {
			return c
		}
	}
	return 0
}

func TestCompareVersionsOrdersByNumericComponentsAndIsAntisymmetric(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := drawVersion(t, "a")
		b := drawVersion(t, "b")
		switch rapid.IntRange(0, 2).Draw(t, "relation") {
		case 1:
			parts := strings.Split(a, ".")
			i := rapid.IntRange(0, 2).Draw(t, "pad")
			parts[i] = strings.Repeat("0", rapid.IntRange(1, 3).Draw(t, "zeros")) + parts[i]
			b = strings.Join(parts, ".")
		case 2:
			parts := strings.Split(a, ".")
			i := rapid.IntRange(0, 2).Draw(t, "differ")
			parts[i] = versionComponent.Draw(t, "other")
			b = strings.Join(parts, ".")
		}
		got := CompareVersions(a, b)
		if want := numericOrder(a, b); got != want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d: components compare as numbers, major before minor before patch, so a pinned tool's supported range admits exactly the versions it names", a, b, got, want)
		}
		if back := CompareVersions(b, a); back != -got {
			t.Fatalf("CompareVersions(%q, %q) = %d but (%q, %q) = %d; an order that depends on argument position makes range checks disagree with pin-equality checks", a, b, got, b, a, back)
		}
	})
}
