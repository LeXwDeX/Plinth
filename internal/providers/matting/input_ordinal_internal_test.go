package matting

import "testing"

// #284: the position of a picture among the submitted ones is read back from
// the name the service echoes, by the Offering's own upload filename template.
func TestInputOrdinalReadsTheUploadNameBack(t *testing.T) {
	for _, test := range []struct {
		name     string
		template string
		source   string
		want     int
	}{
		{"shipped template", "input_{ordinal}{extension}", "input_3.png", 3},
		{"two digits", "input_{ordinal}{extension}", "input_12.jpg", 12},
		{"no extension echoed", "input_{ordinal}{extension}", "input_2", 2},
		{"another template", "scan-{ordinal}-src{extension}", "scan-4-src.webp", 4},
		{"regexp characters in the template are literal", "in(put).{ordinal}{extension}", "in(put).5.png", 5},
		{"a name the template does not produce", "input_{ordinal}{extension}", "renamed.png", 0},
		{"a prefix match is not a match", "input_{ordinal}{extension}", "xinput_1.png", 0},
		{"a trailing suffix is not a match", "input_{ordinal}{extension}", "input_1.png.bak", 0},
		{"zero is not a position", "input_{ordinal}{extension}", "input_0.png", 0},
		{"no digits", "input_{ordinal}{extension}", "input_x.png", 0},
		{"no echoed name", "input_{ordinal}{extension}", "", 0},
		{"no template", "", "input_1.png", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := inputOrdinal(test.source, test.template); got != test.want {
				t.Fatalf("inputOrdinal(%q, %q) = %d, want %d", test.source, test.template, got, test.want)
			}
		})
	}
}
