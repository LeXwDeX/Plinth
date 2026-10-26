// art-ai-retention is an offline, explicitly human-approved maintenance tool.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"golang.org/x/sys/unix"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("choose prepare, apply, cleanup, or receipt")
	}
	mode := args[0]
	if mode != "prepare" && mode != "apply" && mode != "cleanup" && mode != "receipt" {
		return errors.New("unknown retention command")
	}
	f := flag.NewFlagSet("art-ai-retention "+mode, flag.ContinueOnError)
	database := f.String("database", "", "existing absolute SQLite database path")
	storage := f.String("storage", "", "existing absolute CAS root")
	catalogPath := f.String("catalog", "", "explicit private Product Schema file")
	cutoff := f.String("cutoff", "", "explicit RFC3339 cutoff (prepare only)")
	manifestPath := f.String("manifest", "", "absolute manifest path; prepare creates it exclusively")
	hash := f.String("sha256", "", "exact reviewed manifest SHA-256")
	backup := f.String("backup", "", "new absolute backup directory (apply only)")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 || *database == "" {
		return errors.New("explicit --database is required; positional arguments are not accepted")
	}
	if mode == "receipt" {
		if !validHash(*hash) || *storage != "" || *catalogPath != "" || *cutoff != "" || *manifestPath != "" || *backup != "" {
			return errors.New("receipt accepts only --database and --sha256")
		}
		r, e := sqlite.ReadRetentionReceipt(ctx, *database, *hash)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(r)
	}
	if *storage == "" || *catalogPath == "" || *manifestPath == "" {
		return errors.New("explicit --storage, --catalog and --manifest are required")
	}
	if mode == "prepare" && (*cutoff == "" || *hash != "" || *backup != "") {
		return errors.New("prepare requires --cutoff and does not accept --sha256 or --backup")
	}
	if mode != "prepare" && (!validHash(*hash) || *cutoff != "" || mode == "apply" && *backup == "" || mode == "cleanup" && *backup != "") {
		return errors.New("apply/cleanup requires exact --sha256; only apply requires --backup; cutoff comes from manifest")
	}
	// Refuse source aliases before creating any manifest, backup or lock file.
	if e := separateArtifact(*manifestPath, *database, *database+"-wal", *database+"-shm", *database+"-journal", *storage); e != nil {
		return e
	}
	if mode == "apply" {
		if e := separateArtifact(*backup, *database, *database+"-wal", *database+"-shm", *database+"-journal", *storage); e != nil {
			return e
		}
	}
	raw, e := os.ReadFile(*catalogPath)
	if e != nil {
		return e
	}
	cat, e := catalog.Load(bytes.NewReader(raw))
	if e != nil {
		return e
	}
	policy := cat.Application().Runtime.TaskRetention
	if e = policy.Validate(); e != nil {
		return e
	}
	if !policy.Enabled {
		return errors.New("task retention is disabled in the supplied Product Schema")
	}
	var m generation.RetentionManifest
	if mode != "prepare" {
		b, e := local.ReadRetentionArtifact(*manifestPath)
		if e != nil {
			return e
		}
		m, e = generation.DecodeRetentionManifest(b, *hash)
		if e != nil {
			return e
		}
	}
	files, e := local.OpenRetentionFiles(*storage)
	if e != nil {
		return e
	}
	defer files.Close()
	dbDevice, dbInode, e := local.RetentionPathIdentity(*database, false)
	if e != nil {
		return e
	}
	session, e := sqlite.OpenRetentionSession(ctx, *database)
	if e != nil {
		return e
	}
	defer session.Close()
	svc := generation.RetentionService{Repository: session, Files: files, Approver: terminalApprover{}, Policy: policy, Identity: generation.RetentionIdentity{Database: *database, Storage: *storage, DatabaseDevice: dbDevice, DatabaseInode: dbInode, StorageDevice: files.Device, StorageInode: files.Inode}}
	if mode == "prepare" {
		stamp, e := time.Parse(time.RFC3339Nano, *cutoff)
		if e != nil {
			return e
		}
		m, e = svc.Prepare(ctx, stamp)
		if e != nil {
			return e
		}
		b, e := json.Marshal(m)
		if e != nil {
			return e
		}
		if e = local.WriteRetentionArtifact(*manifestPath, b); e != nil {
			return e
		}
		digest, e := m.Digest()
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(struct {
			Manifest string `json:"manifest"`
			SHA256   string `json:"sha256"`
			Tasks    int    `json:"tasks"`
			Inputs   int    `json:"inputs"`
			Files    int    `json:"files"`
			Retained int    `json:"retained"`
		}{*manifestPath, digest, len(m.Tasks), len(m.Inputs), len(m.Files), len(m.Retained)})
	}
	var result generation.RetentionResult
	if mode == "apply" {
		result, e = svc.Apply(ctx, m, *backup)
	} else {
		result, e = svc.Cleanup(ctx, m)
	}
	return errors.Join(e, json.NewEncoder(out).Encode(result))
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func separateArtifact(path string, sources ...string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("retention artifacts require absolute clean paths")
	}
	for _, s := range sources {
		if !filepath.IsAbs(s) || filepath.Clean(s) != s {
			return errors.New("source paths require absolute clean paths")
		}
		if path == s || strings.HasPrefix(path, s+"/") || strings.HasPrefix(s, path+"/") {
			return errors.New("retention artifact overlaps source data")
		}
	}
	_, _, e := local.RetentionPathIdentity(filepath.Dir(path), true)
	return e
}

// No --yes, --approved-by, stdin pipe or environment flag can substitute for
// the independent human decision. Test approvers exist only in Go test files.
type terminalApprover struct{}

func (terminalApprover) Approve(ctx context.Context, hash, purpose string) (generation.RetentionApproval, error) {
	var a generation.RetentionApproval
	if e := ctx.Err(); e != nil {
		return a, e
	}
	tty, e := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if e != nil {
		return a, errors.New("a human operator must approve in an interactive controlling terminal")
	}
	defer tty.Close()
	if _, err := unix.IoctlGetWinsize(int(tty.Fd()), unix.TIOCGWINSZ); err != nil {
		return a, errors.New("approval input is not a terminal")
	}
	_, e = fmt.Fprintf(tty, "Human operator approval required for retention %s.\nReview the immutable manifest and backups scope first. Automation must not answer this prompt.\nManifest SHA-256: %s\nEnter your operator identity: ", purpose, hash)
	if e != nil {
		return a, e
	}
	reader := bufio.NewReader(io.LimitReader(tty, 4096))
	actor, e := approvalLine(ctx, reader)
	if e != nil {
		return a, e
	}
	actor = strings.TrimSpace(actor)
	if actor == "" || len(actor) > 256 {
		return a, errors.New("operator identity required")
	}
	if _, e = fmt.Fprintf(tty, "Type exactly: APPROVE %s %s\n> ", purpose, hash); e != nil {
		return a, e
	}
	answer, e := approvalLine(ctx, reader)
	if e != nil {
		return a, e
	}
	if strings.TrimSpace(answer) != "APPROVE "+purpose+" "+hash {
		return a, errors.New("human approval declined")
	}
	if e = ctx.Err(); e != nil {
		return a, e
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return a, e
	}
	return generation.RetentionApproval{ManifestHash: hash, Actor: actor, Nonce: hex.EncodeToString(nonce), Purpose: purpose, ApprovedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

func approvalLine(ctx context.Context, reader *bufio.Reader) (string, error) {
	type line struct {
		value string
		err   error
	}
	result := make(chan line, 1)
	go func() { value, err := reader.ReadString('\n'); result <- line{value, err} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case got := <-result:
		return got.value, got.err
	}
}
