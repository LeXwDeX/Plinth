package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"testing"
)

func TestRetentionCLIRejectsImplicitAndMachineApproval(t *testing.T) {
	for _, args := range [][]string{nil, {"purge"}, {"prepare"}, {"apply", "--approved-by", "agent"}, {"apply", "--yes"}, {"cleanup", "--database", "missing", "--sha256", "invalid"}, {"receipt", "--database", "missing", "--sha256", "invalid"}} {
		if e := run(context.Background(), args, io.Discard); e == nil {
			t.Fatalf("unsafe invocation accepted %v", args)
		}
	}
}

func TestRetentionApprovalCancellationReleasesPrompt(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := approvalLine(ctx, bufio.NewReader(reader)); !errors.Is(e, context.Canceled) {
		t.Fatalf("prompt did not cancel: %v", e)
	}
}
