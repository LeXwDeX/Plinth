package generationtool

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #284: the web client's canvas size notice computes the size a canvas turn
// sends from the same Schema declarations (src/lib/workspace/canvas-size.ts,
// canvasTurnOutput). Both read testdata/canvas_turn_sizes.json, so the notice
// and the submitted size cannot drift apart, including a canvas above the
// model's maximum (the nearest legal size). #296: the cases with source
// "reference" are a chat edit of one uploaded picture with no size chosen; the
// client computes them with referenceTurnOutput.
func TestCanvasTurnSizesSharedWithTheWebClient(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{chat: product.Application().Chat}
	raw, err := os.ReadFile("testdata/canvas_turn_sizes.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Source, Offering, Parameter, Value string
			Width, Height                            int
			Nearest                                  bool
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("fixture: %v", err)
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			offering, ok := product.Get(generation.OfferingID(test.Offering))
			if !ok {
				t.Fatalf("offering %s is not in the default Schema", test.Offering)
			}
			// The canvas and a single reference pick by the same rule.
			input := map[string]any{}
			for name, value := range referenceSizeValues(offering, test.Width, test.Height) {
				input[name] = value
			}
			// The submitted input is normalised with the Offering's forced values.
			schema.ApplyForcedValues(offering.Parameters, input)
			if input[test.Parameter] != test.Value {
				t.Fatalf("%s = %v; want %s (input %v)", test.Parameter, input[test.Parameter], test.Value, input)
			}
			if test.Source == "reference" {
				template := executor.chat.Context.ReferenceOutputSizeTemplate
				if test.Nearest {
					template = executor.chat.Context.ReferenceOutputSizeNearestTemplate
				}
				reference := fmt.Sprintf("%d×%d", test.Width, test.Height)
				want := strings.NewReplacer("{reference}", reference, "{size}", test.Value).Replace(template)
				if got := executor.referenceSizeNoteFor(offering, input, test.Width, test.Height, nil); got != want {
					t.Fatalf("note = %q\nwant   %q", got, want)
				}
				return
			}
			submitted, _ := json.Marshal(input)
			command := canvasCommand(executor.chat, test.Width, test.Height)
			template := executor.chat.Context.CanvasOutputSizeTemplate
			if test.Nearest {
				template = executor.chat.Context.CanvasOutputSizeNearestTemplate
			}
			canvas := fmt.Sprintf("%d×%d", test.Width, test.Height)
			want := strings.NewReplacer("{canvas}", canvas, "{size}", test.Value).Replace(template)
			if got := executor.canvasSizeNote(command, offering, submitted); got != want {
				t.Fatalf("note = %q\nwant   %q", got, want)
			}
		})
	}
}
