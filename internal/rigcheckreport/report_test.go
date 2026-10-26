package rigcheckreport

import (
	"bytes"
	"strings"
	"testing"
)

func TestReportFalseAndRequiredSpecies(t *testing.T) {
	for _, species := range []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic"} {
		raw, err := Marshal(Report{Format: Format, Riggable: false, RigType: species})
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(raw)
		if err != nil || got.Riggable || got.RigType != species || !bytes.Contains(raw, []byte(`"riggable":false`)) {
			t.Fatalf("false or species lost: %s %v", raw, err)
		}
	}
}
func TestReportRejectsInvalidProtocol(t *testing.T) {
	for _, raw := range []string{
		`{"format":"art-ai.rig-check-report.v1","rig_type":"biped"}`,
		`{"format":"art-ai.rig-check-report.v1","riggable":null,"rig_type":"biped"}`,
		`{"format":"art-ai.rig-check-report.v1","riggable":"false","rig_type":"biped"}`,
		`{"format":"art-ai.rig-check-report.v1","riggable":true,"riggable":false,"rig_type":"biped"}`,
		`{"format":"art-ai.rig-check-report.v2","riggable":true,"rig_type":"biped"}`,
		`{"riggable":true,"rig_type":"biped"}`,
		`{"format":"art-ai.rig-check-report.v1","riggable":true,"rig_type":"biped","url":"https://secret"}`,
		`{"format":"art-ai.rig-check-report.v1","Riggable":true,"rig_type":"biped"}`,
		`{"format":"art-ai.rig-check-report.v1","riggable":true,"rig_type":"biped"} {}`,
		strings.Repeat(" ", MaxBytes+1),
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Errorf("accepted %q", raw[:min(len(raw), 150)])
		}
	}
	if err := ValidateReader(strings.NewReader(`{"format":"art-ai.rig-check-report.v1","riggable":false,"rig_type":"biped"}`), 10); err == nil {
		t.Fatal("reader exceeded limit")
	}
}

func TestReportRequiresLegalRigTypeForEitherRiggability(t *testing.T) {
	for _, riggable := range []string{"true", "false"} {
		for _, field := range []string{"", `,"rig_type":null`, `,"rig_type":""`, `,"rig_type":"unknown"`} {
			raw := `{"format":"art-ai.rig-check-report.v1","riggable":` + riggable + field + `}`
			t.Run(raw, func(t *testing.T) {
				if _, err := Decode([]byte(raw)); err == nil {
					t.Fatal("accepted missing or invalid required rig_type")
				}
			})
		}
		for _, species := range []string{"", "unknown"} {
			if _, err := Marshal(Report{Format: Format, Riggable: riggable == "true", RigType: species}); err == nil {
				t.Errorf("Marshal accepted required rig_type %q", species)
			}
		}
	}
}
