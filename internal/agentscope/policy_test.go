package agentscope

import "testing"

func testPolicy() Policy {
	return Policy{
		DefaultMode: ModeAuto,
		Modes: []Mode{
			{ID: Mode2D, Allows: []string{Class2D, ClassUtility}},
			{ID: Mode3D, Allows: []string{Class3D, ClassUtility}},
			{ID: ModeAuto, Allows: []string{Class2D, Class3D, ClassCross, ClassUtility}},
		},
	}
}

func TestToolAllowedMatrix(t *testing.T) {
	policy := testPolicy()
	want := map[string]map[string]bool{
		Mode2D:   {Class2D: true, Class3D: false, ClassCross: false, ClassUtility: true},
		Mode3D:   {Class2D: false, Class3D: true, ClassCross: false, ClassUtility: true},
		ModeAuto: {Class2D: true, Class3D: true, ClassCross: true, ClassUtility: true},
	}
	for scope, classes := range want {
		for class, allowed := range classes {
			if got := policy.ToolAllowed(scope, class); got != allowed {
				t.Errorf("scope %s class %s = %v, want %v", scope, class, got, allowed)
			}
		}
	}
}

func TestUnknownClassIsTreatedAsCross(t *testing.T) {
	policy := testPolicy()
	for _, class := range []string{"", "mystery"} {
		if policy.ToolAllowed(Mode2D, class) || policy.ToolAllowed(Mode3D, class) {
			t.Errorf("class %q is exposed in a restricted scope", class)
		}
		if !policy.ToolAllowed(ModeAuto, class) {
			t.Errorf("class %q is hidden in auto", class)
		}
	}
}

func TestNormalizeFallsBackToTheSchemaDefault(t *testing.T) {
	policy := testPolicy()
	for _, stored := range []string{"", "AUTO", "4d", " 2d"} {
		if got := policy.Normalize(stored); got != ModeAuto {
			t.Errorf("Normalize(%q) = %q, want the default", stored, got)
		}
	}
	if got := policy.Normalize(Mode3D); got != Mode3D {
		t.Fatalf("Normalize(3d) = %q", got)
	}
	// A scope the Schema stopped offering applies the default's rules.
	policy.Modes = policy.Modes[:2]
	policy.DefaultMode = Mode2D
	if policy.Allows(ModeAuto) || policy.Normalize(ModeAuto) != Mode2D {
		t.Fatal("a removed scope must fall back to the default")
	}
}
