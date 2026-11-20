package align

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// fetchCap bounds the bytes read from an artifact URL for an observation.
	fetchCap = 256 << 20
	// savedArtifactCap bounds one artifact kept under Target.ArtifactDir: a model
	// with large textures is far bigger than what an observation needs to read.
	savedArtifactCap = 1 << 30
)

// artifactSave says where the bytes of one fetched artifact are kept.
type artifactSave struct {
	Dir  string // an existing directory the caller has checked (0700, outside the worktree)
	Step string // names the file: <step>-<n>.<ext>
	N    int
}

// headWriter keeps the first formatHead bytes written to it and counts them all,
// which is what detectFormat needs of a file that is not held in memory.
type headWriter struct {
	head  []byte
	total int64
}

func (w *headWriter) Write(p []byte) (int, error) {
	if room := formatHead - len(w.head); room > 0 {
		w.head = append(w.head, p[:min(room, len(p))]...)
	}
	w.total += int64(len(p))
	return len(p), nil
}

// readTracker remembers a read error of the source, which tells a network
// failure from a failure to write the file.
type readTracker struct {
	r   io.Reader
	err error
}

func (t *readTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		t.err = err
	}
	return n, err
}

// keep streams body into a new file of s.Dir (mode 0600, SHA-256 and format
// taken on the way) and fills the observation's size, format, file name and
// digest. A failure to read the body or a body over savedArtifactCap leaves the
// reason in obs.Error, removes the partial file and returns nil: the run goes
// on. A failure to write the file locally is returned.
func (s *artifactSave) keep(obs *ArtifactObs, rawURL string, body io.Reader) error {
	tmp, err := os.CreateTemp(s.Dir, ".fetch-*.part")
	if err != nil {
		obs.Error = "create file: " + err.Error()
		return fmt.Errorf("keep artifact %s-%d: %w", s.Step, s.N, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once the file was renamed into place
	hash, head := sha256.New(), &headWriter{}
	source := &readTracker{r: io.LimitReader(body, savedArtifactCap+1)}
	written, err := io.Copy(io.MultiWriter(tmp, hash, head), source)
	switch {
	case source.err != nil:
		_ = tmp.Close()
		obs.Error = "read body: " + withoutURL(source.err)
		return nil
	case err != nil:
		_ = tmp.Close()
		obs.Error = "write file: " + err.Error()
		return fmt.Errorf("keep artifact %s-%d: %w", s.Step, s.N, err)
	case written > savedArtifactCap:
		_ = tmp.Close()
		obs.Error = fmt.Sprintf("larger than %d bytes: not saved", int64(savedArtifactCap))
		return nil
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		obs.Error = "write file: " + err.Error()
		return fmt.Errorf("keep artifact %s-%d: %w", s.Step, s.N, err)
	}
	if err := tmp.Close(); err != nil {
		obs.Error = "write file: " + err.Error()
		return fmt.Errorf("keep artifact %s-%d: %w", s.Step, s.N, err)
	}
	obs.Size = written
	obs.Format = detectFormat(head.head, head.total)
	obs.SHA256 = hex.EncodeToString(hash.Sum(nil))
	name, err := s.place(tmp.Name(), artifactExtension(obs.Format, rawURL))
	if err != nil {
		obs.SHA256 = ""
		obs.Error = "place file: " + err.Error()
		return fmt.Errorf("keep artifact %s-%d: %w", s.Step, s.N, err)
	}
	obs.File = name
	return nil
}

// place moves the finished file to <step>-<n>.<ext>. A file of that name that is
// already there (an earlier run into the same directory) is never replaced: the
// new bytes go to <step>-<n>-<k>.<ext> with the next free k.
func (s *artifactSave) place(from, ext string) (string, error) {
	base := fileSafe(s.Step) + "-" + strconv.Itoa(s.N)
	for attempt := 1; attempt <= 1000; attempt++ {
		name := base + "." + ext
		if attempt > 1 {
			name = base + "-" + strconv.Itoa(attempt) + "." + ext
		}
		target := filepath.Join(s.Dir, name)
		if _, err := os.Lstat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return name, os.Rename(from, target)
	}
	return "", errors.New("no free file name")
}

// fileSafe keeps a step name usable as a file name prefix: anything but letters,
// digits, dot, dash and underscore becomes an underscore.
func fileSafe(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, name)
}

// containerExtensions are the extensions a URL path may name for bytes
// DetectFormat only calls zip, text or binary: a 3MF or USDZ is a ZIP package.
var containerExtensions = map[string]bool{
	"3mf": true, "usdz": true, "obj": true, "gltf": true, "mtl": true, "splat": true, "ply": true, "json": true,
}

// artifactExtension is the extension of a saved artifact: its detected format's
// own and, for a format DetectFormat leaves generic (zip, text, binary), the
// known container extension its URL path names.
func artifactExtension(format, rawURL string) string {
	switch format {
	case "glb":
		return "glb"
	case "fbx-binary":
		return "fbx"
	case "png":
		return "png"
	case "webp":
		return "webp"
	case "jpeg":
		return "jpg"
	case "stl-ascii", "stl-binary":
		return "stl"
	}
	if parsed, err := url.Parse(rawURL); err == nil {
		if ext := strings.ToLower(strings.TrimPrefix(path.Ext(parsed.Path), ".")); containerExtensions[ext] {
			return ext
		}
	}
	switch format {
	case "zip":
		return "zip"
	case "text":
		return "txt"
	}
	return "bin"
}

// withoutURL is an error's text without the request URL a *url.Error carries:
// an artifact URL is a signed link that must not reach the console or a file.
func withoutURL(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return err.Error()
}
