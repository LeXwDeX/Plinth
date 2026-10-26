package local

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

func TestUSDCOfficialCompressedIntegerAddressRejected(t *testing.T) {
	d := usdcFixture(t, "compressed_integer_address")
	digest := sha256.Sum256(d)
	// This golden is the independently produced and reopened OpenUSD crate.
	// It was accepted by all three delivery boundaries before this regression.
	if hex.EncodeToString(digest[:]) != "986a44f28fd27d4500f0659ea952a01f5c50c6c61356afa8d00be22499a9bcde" || containsAddress(d, absoluteAddress) {
		t.Fatal("independent official compressed integer control changed")
	}
	t.Run("standalone", func(t *testing.T) { usdcDelivery(t, d, "usdc", false) })
	t.Run("aligned_stored_archive", func(t *testing.T) { usdcDelivery(t, usdcTestZIP(t, d, false, false), "usdz", false) })
	// The existing independently written crate contains a normal compressed
	// integer array. Both forms must still pass the same delivery boundaries.
	good := usdcFixture(t, "typed")
	t.Run("normal_standalone", func(t *testing.T) { usdcDelivery(t, good, "usdc", true) })
	t.Run("normal_archive", func(t *testing.T) { usdcDelivery(t, usdcTestZIP(t, good, false, false), "usdz", true) })
}

// Independent test encoder: choose a zero common delta and explicitly write
// every signed delta as four bytes. Production code never creates this wire.
func usdcTestIntegerDeltaBytes(values []byte) ([]byte, int64) {
	count := (len(values) + 3) / 4
	padded := make([]byte, count*4)
	copy(padded, values)
	encoded := make([]byte, 4+(count+3)/4)
	var previous uint32
	for i := 0; i < count; i++ {
		encoded[4+i/4] |= 3 << uint(2*(i%4))
		v := binary.LittleEndian.Uint32(padded[i*4:])
		word := make([]byte, 4)
		binary.LittleEndian.PutUint32(word, v-previous)
		encoded = append(encoded, word...)
		previous = v
	}
	return encoded, int64(count)
}

func usdcTestIntegerRecord(encoded []byte) []byte {
	wire := usdcTestLiterals(encoded)
	return append(usdcTestUint64(uint64(len(wire))), wire...)
}

func TestUSDCReconstructedIntegerAddressesAndWindowCarry(t *testing.T) {
	bigEndian := func(text string) []byte {
		var result []byte
		for _, v := range []byte(text) {
			result = append(result, 0, v)
		}
		return result
	}
	for name, text := range map[string][]byte{
		"plain":          []byte("https://example.invalid/a"),
		"percent":        []byte("%68%74%74%70%73%3a%2f%2fexample.invalid/a"),
		"escaped":        []byte(`https:\u002f\u002fexample.invalid/a`),
		"utf16_little":   usdcTestUTF16("https://example.invalid/a"),
		"utf16_big":      bigEndian("https://example.invalid/a"),
		"numeric_only":   []byte(" //example.invalid/a"),
		"normal_numeric": bytes.Repeat([]byte{1, 0, 0, 0}, 16),
	} {
		for _, prefix := range []int{0, isolationChunkBytes - 3, isolationChunkBytes - 1} {
			values := append(bytes.Repeat([]byte{'x'}, prefix), text...)
			if len(values) < 64 {
				values = append(values, make([]byte, 64-len(values))...)
			}
			encoded, count := usdcTestIntegerDeltaBytes(values)
			if containsAddress(encoded, absoluteAddress) {
				t.Fatalf("%s independent delta wire already contains an absolute address", name)
			}
			for _, collect := range []bool{false, true} {
				p := usdcParser{work: newInspectionWorkBudget()}
				record := usdcTestIntegerRecord(encoded)
				c := usdcCursor{record, 0, int64(len(record))}
				result, e := p.integers(&c, count, collect)
				wantReject := name != "numeric_only" && name != "normal_numeric"
				if (e != nil) != wantReject || e != nil && !strings.Contains(e.Error(), "外部地址") {
					t.Fatalf("%s prefix=%d collect=%v rejection=%v: %v", name, prefix, collect, wantReject, e)
				}
				if e == nil && collect {
					for i, value := range result {
						start := 4 * i
						var word [4]byte
						copy(word[:], values[start:min(start+4, len(values))])
						if value != binary.LittleEndian.Uint32(word[:]) {
							t.Fatal("normal integer values changed")
						}
					}
				}
			}
		}
	}
}

func TestUSDCIntegerDecodeWorkAndFramingFailClosed(t *testing.T) {
	encoded, count := usdcTestIntegerDeltaBytes(bytes.Repeat([]byte{1, 0, 0, 0}, 16))
	record := usdcTestIntegerRecord(encoded)
	p := usdcParser{work: newInspectionWorkBudget()}
	c := usdcCursor{record, 0, int64(len(record))}
	if _, e := p.integers(&c, count, false); e != nil {
		t.Fatal(e)
	}
	cost := maxInspectionWork - p.work.remaining
	// The malformed LZ4 stream would fail in the decoder. Insufficient work
	// must reject before entering it or allocating its decoded output.
	bad := append(usdcTestUint64(2), 0, 0xff)
	p.work = &inspectionWorkBudget{remaining: cost - 1}
	c = usdcCursor{bad, 0, int64(len(bad))}
	if _, e := p.integers(&c, count, false); e == nil || e.Error() != inspectionWorkError().Error() {
		t.Fatalf("decoder ran before complete work reservation: %v", e)
	}
	for name, content := range map[string][]byte{
		"wire_truncated":  record[:len(record)-1],
		"delta_truncated": usdcTestIntegerRecord(encoded[:len(encoded)-4]),
		"delta_trailing":  usdcTestIntegerRecord(append(append([]byte(nil), encoded...), 0)),
	} {
		t.Run(name, func(t *testing.T) {
			p.work = newInspectionWorkBudget()
			c := usdcCursor{content, 0, int64(len(content))}
			if _, e := p.integers(&c, count, false); e == nil {
				t.Fatal("malformed integer record accepted")
			}
		})
	}
	for _, count := range []int64{-1, maxUSDCBytes/4 + 1} {
		p.work = newInspectionWorkBudget()
		c := usdcCursor{record, 0, int64(len(record))}
		if _, e := p.integers(&c, count, false); e == nil {
			t.Fatal("invalid integer count accepted")
		}
	}
	// Preserve the independent absolute scan of the expanded delta encoding.
	abs := append([]byte(nil), encoded...)
	copy(abs, "https://example.invalid/a")
	content := usdcTestIntegerRecord(abs)
	p.work = newInspectionWorkBudget()
	c = usdcCursor{content, 0, int64(len(content))}
	if _, e := p.integers(&c, count, false); e == nil || !strings.Contains(e.Error(), "外部地址") {
		t.Fatalf("encoded absolute address accepted: %v", e)
	}
}
