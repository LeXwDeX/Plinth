package local

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// usdcTestFieldSets appends extra single-field sets to the fixture's FIELDSETS
// table; specs keep referring to the original sets.
func usdcTestFieldSets(t *testing.T, d []byte, extra int) []byte {
	t.Helper()
	var section usdcSection
	for _, s := range usdcTestSections(d) {
		if s.name == "FIELDSETS" {
			section = s
		}
	}
	p := usdcParser{data: d, work: newInspectionWorkBudget(), limit: maxUSDCBytes}
	c := &usdcCursor{d, section.start, section.end}
	count, err := c.u64()
	if err != nil {
		t.Fatal(err)
	}
	sets, err := p.integers(c, int64(count), true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < extra; i++ {
		sets = append(sets, 0, ^uint32(0))
	}
	plain := make([]byte, 4*len(sets))
	for i, v := range sets {
		binary.LittleEndian.PutUint32(plain[i*4:], v)
	}
	encoded, total := usdcTestIntegerDeltaBytes(plain)
	body := append(usdcTestUint64(uint64(total)), usdcTestIntegerRecord(encoded)...)
	return usdcTestRewrite(d, "FIELDSETS", body, nil)
}

// A real multi-part Tripo segmentation USDZ carried 10640 field-set entries;
// table sizes are bounded by the byte and work budgets, not the archive member cap.
func TestUSDCLargeFieldSetTableIsDelivered111(t *testing.T) {
	d := usdcTestFieldSets(t, usdcFixture(t, "typed"), maxArchiveFiles/2+400)
	t.Run("standalone", func(t *testing.T) { usdcDelivery(t, d, "usdc", true) })
	t.Run("archive", func(t *testing.T) { usdcDelivery(t, usdcTestZIP(t, d, false, false), "usdz", true) })
}

func TestUSDCTableEntryCeilingStillRejects111(t *testing.T) {
	for _, name := range []string{"FIELDS", "FIELDSETS", "SPECS"} {
		d := usdcFixture(t, "typed")
		for _, s := range usdcTestSections(d) {
			if s.name == name {
				binary.LittleEndian.PutUint64(d[s.start:], maxModelStructureEntries+1)
			}
		}
		archive := usdcTestZIP(t, d, false, false)
		err := inspectArchive(bytes.NewReader(archive), int64(len(archive)))
		if err == nil || !strings.Contains(err.Error(), "超出限制") {
			t.Fatalf("%s: expected the table ceiling to reject, got %v", name, err)
		}
	}
}
