package local

import (
	"bytes"
	"runtime"
	"testing"
)

func usdcTestTokenRecord(count uint64, decoded []byte) []byte {
	wire := usdcTestLiterals(decoded)
	record := append(usdcTestUint64(count), usdcTestUint64(uint64(len(decoded)))...)
	record = append(record, usdcTestUint64(uint64(len(wire)))...)
	return append(record, wire...)
}

func TestUSDCTokenDeclaredCountsAndExactFraming(t *testing.T) {
	for _, test := range []struct {
		name     string
		count    uint64
		decoded  []byte
		accepted bool
	}{
		{"zero", 0, nil, true},
		{"empty_token", 1, []byte{0}, true},
		{"two_empty_tokens", 2, []byte{0, 0}, true},
		{"text", 2, []byte("a\x00b\x00"), true},
		{"zero_nonempty", 0, []byte{0}, false},
		{"declared_too_few", 1, []byte("a\x00b\x00"), false},
		{"declared_too_many", 2, []byte("a\x00"), false},
		{"missing_terminator", 1, []byte("text"), false},
		{"trailer", 1, []byte("a\x00x"), false},
		{"invalid_utf8", 1, []byte{0xff, 0}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := usdcTestTokenRecord(test.count, test.decoded)
			p := usdcParser{work: newInspectionWorkBudget()}
			c := usdcCursor{d, 0, int64(len(d))}
			e := p.readTokens(&c)
			if (e == nil) != test.accepted {
				t.Fatalf("accepted=%v want=%v: %v", e == nil, test.accepted, e)
			}
			if e == nil && uint64(len(p.tokens)) != test.count {
				t.Fatal("declared token count changed")
			}
		})
	}
	// Independent official fixture keeps both standalone and archive readers
	// honest while the malformed table transformations reject at all boundaries.
	good := usdcFixture(t, "finite_vec2f")
	usdcDelivery(t, good, "usdc", true)
	usdcDelivery(t, usdcTestZIP(t, good, false, false), "usdz", true)
	for _, record := range [][]byte{
		usdcTestTokenRecord(1, []byte("a\x00b\x00")),
		usdcTestTokenRecord(1, bytes.Repeat([]byte{0}, 1<<16)),
		append(usdcTestTokenRecord(1, []byte("a\x00")), 0),
	} {
		bad := usdcTestRewrite(good, "TOKENS", record, nil)
		usdcDelivery(t, bad, "usdc", false)
		usdcDelivery(t, usdcTestZIP(t, bad, false, false), "usdz", false)
	}
}

func TestUSDCTokenNULCountCannotMultiplyDescriptorAllocation(t *testing.T) {
	// With one declared token, the old full Split allocated 1,048,577 slice
	// descriptors (24MiB on 64-bit hosts). Measure bytes, not just allocations
	// or the rejection message. The bounded parser needs only decoded output
	// and two descriptors; allow additional runtime workspace conservatively.
	d := usdcTestTokenRecord(1, bytes.Repeat([]byte{0}, 1<<20))
	p := usdcParser{work: newInspectionWorkBudget()}
	c := usdcCursor{d, 0, int64(len(d))}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	e := p.readTokens(&c)
	runtime.ReadMemStats(&after)
	if e == nil {
		t.Fatal("undeclared empty tokens accepted")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
		t.Fatalf("token delimiters multiplied descriptor allocation: %d bytes", allocated)
	}
	if len(p.tokens) != 0 {
		t.Fatal("token slice allocated before count validation")
	}
}

func TestUSDCTokenAllocationWorkReservedBeforeDecoder(t *testing.T) {
	good := usdcTestTokenRecord(1, []byte("a\x00"))
	p := usdcParser{work: newInspectionWorkBudget()}
	c := usdcCursor{good, 0, int64(len(good))}
	if e := p.readTokens(&c); e != nil {
		t.Fatal(e)
	}
	cost := maxInspectionWork - p.work.remaining
	// Same declared count and decoded size, with deliberately invalid LZ4.
	// A decoder error would prove the reservation happened too late.
	bad := append(usdcTestUint64(1), usdcTestUint64(2)...)
	bad = append(bad, usdcTestUint64(2)...)
	bad = append(bad, 0, 0xff)
	p.work = &inspectionWorkBudget{remaining: cost - 1}
	c = usdcCursor{bad, 0, int64(len(bad))}
	if e := p.readTokens(&c); e == nil || e.Error() != inspectionWorkError().Error() {
		t.Fatalf("decoder ran before token allocation reservation: %v", e)
	}
}
