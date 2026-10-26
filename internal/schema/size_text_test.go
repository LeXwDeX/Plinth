package schema

import "testing"

// #327: sizes are decimal MB (1,000,000 bytes) everywhere — Schema limits, the page and the
// Agent's tool results — so the 91.8 MB file the page lists is never told as 87.5 MB (its MiB).
func TestSizeTextIsDecimalAndNeverShowsAnOversizedFileAsItsLimit(t *testing.T) {
	for _, test := range []struct {
		bytes   int64
		roundUp bool
		want    string
	}{
		// The dev-test Bang result (#321 note 99121): 91.8 MB, 87.5 MiB.
		{91_800_000, true, "91.8 MB"},
		{91_754_311, true, "91.76 MB"},
		// #276: the 2048² highest-quality PNG over a 10 MiB limit.
		{10_516_505, true, "10.52 MB"},
		{10_485_760, false, "10.48 MB"},
		{10_485_761, true, "10.49 MB"},
		{52_428_800, false, "52.42 MB"},
		{20_000_000, false, "20 MB"},
		{20_000_001, true, "20.01 MB"},
		{150_000_000, false, "150 MB"},
		{4097, true, "4.1 KB"},
		{1000, false, "1 KB"},
		{999_999, true, "1000 KB"},
		{0, true, "0 KB"},
	} {
		if got := SizeText(test.bytes, test.roundUp); got != test.want {
			t.Fatalf("SizeText(%d, %v) = %q, want %q", test.bytes, test.roundUp, got, test.want)
		}
	}
	got := FillSizeRefusal("{label} {size} > {limit}", "参考图", 10_516_505, 10_485_760)
	if got != "参考图 10.52 MB > 10.48 MB" {
		t.Fatalf("FillSizeRefusal = %q", got)
	}
}
