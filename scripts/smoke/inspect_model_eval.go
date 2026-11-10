//go:build ignore

// inspect_model_eval prints the same JSON-ready model facts the Agent's
// inspect_model tool uses, for the #238 output evaluation. It is run as
//
//	go run scripts/smoke/inspect_model_eval.go <model.glb>
//
// from the repository root; the build tag keeps it out of normal builds.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run inspect_model_eval.go <model.glb>")
		os.Exit(2)
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	model, err := modelinspect.Parse(file, info.Size(), modelinspect.Limits{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report, err := modelinspect.InspectContext(context.Background(), model, modelinspect.DefaultInspectOptions())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
