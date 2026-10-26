package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestArgumentsRequireExactBoundedPair(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--database", "db"},
		{"--database", "db", "--storage-root", "store", "--owner", "o", "--failed-task", "a", "--delivery-task", "a"},
		{"--database", "db", "--storage-root", "store", "--owner", "o", "--failed-task", "a"},
	} {
		if _, err := parseArguments(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted insecure arguments: %v", args)
		}
	}
	var output bytes.Buffer
	var stderr bytes.Buffer
	got, err := parseArguments([]string{"--database", "db", "--storage-root", "store", "--owner", "o", "--failed-task", "failed", "--delivery-task", "delivered"}, &stderr)
	if err != nil || got.apply || got.failedTask != "failed" || got.deliveryTask != "delivered" {
		t.Fatalf("dry-run defaults = %#v, %v (%s)", got, err, stderr.String())
	}
	got, err = parseArguments([]string{"--database", "db", "--storage-root", "store", "--owner", "o", "--failed-task", "failed", "--delivery-task", "delivered", "--apply"}, &output)
	if err != nil || !got.apply {
		t.Fatalf("apply = %#v, %v", got, err)
	}
	if strings.Contains(output.String(), "apply") {
		t.Fatalf("unexpected flag output: %s", output.String())
	}
}
