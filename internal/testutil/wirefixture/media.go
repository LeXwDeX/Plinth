package wirefixture

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

type MediaLocation struct {
	Sequence int `json:"sequence"`
	// Part is the part index in a multipart request, or -1 for a response body.
	Part int `json:"part"`
}

// MediaEvidence is private and intentionally not part of Exchange.
type MediaEvidence struct {
	Location MediaLocation
	Source   *PrivateSource
}

func (e MediaEvidence) String() string {
	return fmt.Sprintf("wirefixture.MediaEvidence{Location:%+v, Source:<private>}", e.Location)
}

func (e MediaEvidence) GoString() string {
	return e.String()
}

// MediaResolver is sealed: its unexported method prevents external
// implementations from bypassing digest, MIME and source checks.
type MediaResolver interface {
	resolve(location MediaLocation, media Media) ([]byte, error)
}

type SyntheticMediaResolver struct{}

func (SyntheticMediaResolver) Resolve(_ MediaLocation, media Media) ([]byte, error) {
	if media.Recipe == "" || !media.Substituted {
		return nil, fmt.Errorf("%w: synthetic resolver requires derived media", ErrMediaResolver)
	}
	expected, data, err := SyntheticMedia(media.Recipe)
	if err != nil {
		return nil, err
	}
	if media.MediaType != expected.MediaType || media.Length != expected.Length {
		return nil, fmt.Errorf("%w: synthetic media descriptor", ErrMediaMismatch)
	}
	return data, nil
}

func (s SyntheticMediaResolver) resolve(location MediaLocation, media Media) ([]byte, error) {
	return s.Resolve(location, media)
}

func recipeForMedia(media Media, requestPath string, byMediaType, byExtension map[string]string) (string, bool) {
	if requestPath != "" {
		ext := strings.ToLower(path.Ext(requestPath))
		if recipe, ok := byExtension[ext]; ok && ext != "" {
			return recipe, true
		}
	}
	recipe, ok := byMediaType[media.MediaType]
	return recipe, ok
}

// SubstituteMediaWithExtensions replaces captured media descriptors with fixed
// synthetic recipes before public export. Request media is selected by type;
// response media may be selected by the public request path extension. Unknown
// captured types fail closed. The original bytes remain private evidence.
func SubstituteMediaWithExtensions(exchanges []Exchange, byMediaType, byExtension map[string]string) ([]Exchange, error) {
	out := sortedExchanges(exchanges)
	for i := range out {
		changed := false
		if media := out[i].Response.Body.Media; media != nil && media.Recipe == "" {
			recipe, ok := recipeForMedia(*media, out[i].Request.Path, byMediaType, byExtension)
			if !ok {
				return nil, fmt.Errorf("%w: no synthetic recipe for %s", ErrMediaResolver, media.MediaType)
			}
			synthetic, _, err := SyntheticMedia(recipe)
			if err != nil {
				return nil, err
			}
			out[i].Response.Body.Media = &synthetic
			out[i].Response.ContentType = synthetic.MediaType
			changed = true
		}
		if multipart := out[i].Request.Body.Multipart; multipart != nil {
			for j := range multipart.Parts {
				media := multipart.Parts[j].Media
				if media == nil || media.Recipe != "" {
					continue
				}
				recipe, ok := byMediaType[media.MediaType]
				if !ok {
					return nil, fmt.Errorf("%w: no synthetic recipe for %s", ErrMediaResolver, media.MediaType)
				}
				synthetic, _, err := SyntheticMedia(recipe)
				if err != nil {
					return nil, err
				}
				multipart.Parts[j].Media = &synthetic
				changed = true
			}
		}
		if changed {
			out[i].Metadata.Provenance = ProvenanceDerived
		}
	}
	return out, nil
}

type MediaEvidenceRecord struct {
	Sequence  int    `json:"sequence"`
	Part      int    `json:"part"`
	MediaType string `json:"media_type"`
	Length    int64  `json:"length"`
	SHA256    string `json:"sha256"`
}

// WriteEvidenceManifest lists captured bytes by location without exposing them.
// The destination must be a new regular 0600 file in an existing 0700 directory.
func WriteEvidenceManifest(path string, evidence []MediaEvidence) error {
	if len(evidence) == 0 {
		return nil
	}
	if path == "" || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return fmt.Errorf("%w: evidence manifest path", ErrMediaEvidence)
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%w: evidence manifest directory", ErrMediaEvidence)
	}
	records := make([]MediaEvidenceRecord, 0, len(evidence))
	for _, item := range evidence {
		if item.Source == nil {
			return fmt.Errorf("%w: evidence source", ErrMediaEvidence)
		}
		length, err := item.Source.Stat()
		if err != nil {
			return fmt.Errorf("%w: evidence stat", ErrMediaEvidence)
		}
		records = append(records, MediaEvidenceRecord{
			Sequence: item.Location.Sequence, Part: item.Location.Part,
			MediaType: item.Source.mediaType, Length: length,
			SHA256: hex.EncodeToString(item.Source.digest[:]),
		})
	}
	data, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("%w: evidence manifest encode", ErrMediaEvidence)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%w: evidence manifest create", ErrMediaEvidence)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("%w: evidence manifest write", ErrMediaEvidence)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: evidence manifest close", ErrMediaEvidence)
	}
	written, err := os.Lstat(path)
	if err != nil || !written.Mode().IsRegular() || written.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: evidence manifest mode", ErrMediaEvidence)
	}
	ok = true
	return nil
}

// PrivateStore owns captured bytes outside the worktree through a pinned
// os.Root. Path identity is checked when a sink is created; file reads use the
// pinned root with no-follow and non-blocking flags. Hostile same-user races
// after those checks remain outside this test helper's guarantee.
type PrivateStore struct {
	mu      sync.Mutex
	dir     *os.Root
	dirAbs  string
	dirInfo os.FileInfo
	closed  bool
}

func NewPrivateStore(worktreeRoot, dir string) (*PrivateStore, error) {
	if strings.TrimSpace(worktreeRoot) == "" || strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("%w: worktree and evidence directory are required", ErrMediaEvidence)
	}
	rootAbs, err := filepath.Abs(worktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: worktree path", ErrMediaEvidence)
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: evidence path", ErrMediaEvidence)
	}
	rootInfo, err := os.Stat(rootAbs)
	if err != nil || !rootInfo.IsDir() {
		return nil, fmt.Errorf("%w: worktree root", ErrMediaEvidence)
	}
	dirInfo, err := os.Lstat(dirAbs)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || dirInfo.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("%w: evidence directory must be a real 0700 directory", ErrMediaEvidence)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, fmt.Errorf("%w: worktree identity", ErrMediaEvidence)
	}
	dirReal, err := filepath.EvalSymlinks(dirAbs)
	if err != nil {
		return nil, fmt.Errorf("%w: evidence identity", ErrMediaEvidence)
	}
	if inside(rootReal, dirReal) || inside(dirReal, rootReal) {
		return nil, fmt.Errorf("%w: evidence directory overlaps worktree", ErrInsideWorktree)
	}
	if same, err := sameFileAncestor(rootReal, dirReal); err != nil || same {
		return nil, fmt.Errorf("%w: evidence directory identity overlaps worktree", ErrInsideWorktree)
	}
	if same, err := sameFileAncestor(dirReal, rootReal); err != nil || same {
		return nil, fmt.Errorf("%w: evidence directory identity overlaps worktree", ErrInsideWorktree)
	}
	if err := rejectBindMountAliases(rootReal, dirReal); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dirReal)
	if err != nil {
		return nil, fmt.Errorf("%w: evidence directory handle", ErrMediaEvidence)
	}
	pinned, err := root.Stat(".")
	if err != nil || !pinned.IsDir() || !os.SameFile(dirInfo, pinned) {
		_ = root.Close()
		return nil, fmt.Errorf("%w: evidence directory identity", ErrMediaEvidence)
	}
	return &PrivateStore{dir: root, dirAbs: dirReal, dirInfo: pinned}, nil
}

func (s *PrivateStore) NewSink(mediaType string) (*PrivateSink, error) {
	if s == nil || !supportedMediaType(mediaType) {
		return nil, fmt.Errorf("%w: unsupported media type", ErrUnsupportedBody)
	}
	s.mu.Lock()
	if s.closed || s.dir == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: evidence store closed", ErrMediaEvidence)
	}
	root, dirAbs, expected := s.dir, s.dirAbs, s.dirInfo
	s.mu.Unlock()
	pathInfo, err := os.Lstat(dirAbs)
	if err != nil || !pathInfo.IsDir() || pathInfo.Mode().Perm() != 0o700 || !os.SameFile(expected, pathInfo) {
		return nil, fmt.Errorf("%w: evidence directory changed", ErrMediaEvidence)
	}
	pinned, err := root.Stat(".")
	if err != nil || !pinned.IsDir() || !os.SameFile(expected, pinned) {
		return nil, fmt.Errorf("%w: evidence directory changed", ErrMediaEvidence)
	}
	for attempts := 0; attempts < 8; attempts++ {
		name, err := randomName()
		if err != nil {
			return nil, err
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, fmt.Errorf("%w: create evidence", ErrMediaEvidence)
		}
		fileInfo, err := file.Stat()
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != 0o600 {
			_ = file.Close()
			_ = root.Remove(name)
			return nil, fmt.Errorf("%w: evidence file mode", ErrMediaEvidence)
		}
		return &PrivateSink{file: file, dir: root, name: name, mediaType: mediaType, digest: sha256.New()}, nil
	}
	return nil, fmt.Errorf("%w: create evidence", ErrMediaEvidence)
}

func (s *PrivateStore) Spool(mediaType string, data []byte) (*PrivateSource, error) {
	sink, err := s.NewSink(mediaType)
	if err != nil {
		return nil, err
	}
	if _, err := sink.Write(data); err != nil {
		sink.Abort()
		return nil, err
	}
	source, err := sink.Source()
	if err != nil {
		return nil, err
	}
	return source, nil
}

func (s *PrivateStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.dir == nil {
		return nil
	}
	if err := s.dir.Close(); err != nil {
		return errors.New("wirefixture: private store close failed")
	}
	return nil
}

func (s *PrivateStore) String() string {
	return "wirefixture.PrivateStore{redacted}"
}

func (s *PrivateStore) GoString() string {
	return s.String()
}

func randomName() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("%w: evidence name", ErrMediaEvidence)
	}
	return "media-" + hex.EncodeToString(raw[:]) + ".bin", nil
}

type PrivateSink struct {
	mu        sync.Mutex
	file      *os.File
	dir       *os.Root
	name      string
	mediaType string
	written   int64
	digest    hash.Hash
	closed    bool
}

func (s *PrivateSink) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return 0, fmt.Errorf("%w: evidence sink closed", ErrMediaEvidence)
	}
	n, err := s.file.Write(data)
	if n > 0 {
		_, _ = s.digest.Write(data[:n])
		s.written += int64(n)
	}
	if err != nil || n != len(data) {
		return n, fmt.Errorf("%w: evidence write", ErrMediaEvidence)
	}
	return n, nil
}

func (s *PrivateSink) Source() (*PrivateSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return nil, fmt.Errorf("%w: evidence sink closed", ErrMediaEvidence)
	}
	s.closed = true
	file := s.file
	s.file = nil
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		_ = file.Close()
		_ = s.dir.Remove(s.name)
		return nil, fmt.Errorf("%w: evidence file", ErrMediaEvidence)
	}
	var digest [sha256.Size]byte
	copy(digest[:], s.digest.Sum(nil))
	if err := file.Close(); err != nil {
		_ = s.dir.Remove(s.name)
		return nil, fmt.Errorf("%w: evidence close", ErrMediaEvidence)
	}
	return &PrivateSource{dir: s.dir, name: s.name, mediaType: s.mediaType, length: s.written, digest: digest}, nil
}

func (s *PrivateSink) Abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	if s.dir != nil {
		_ = s.dir.Remove(s.name)
	}
}

func (s *PrivateSink) String() string {
	return "wirefixture.PrivateSink{redacted}"
}

func (s *PrivateSink) GoString() string {
	return s.String()
}

type PrivateSource struct {
	dir       *os.Root
	name      string
	mediaType string
	length    int64
	digest    [sha256.Size]byte
}

func (s *PrivateSource) Bytes() ([]byte, error) {
	if s == nil || s.dir == nil {
		return nil, fmt.Errorf("%w: source is nil", ErrMediaEvidence)
	}
	file, err := s.dir.OpenFile(s.name, os.O_RDONLY|noFollow|nonBlock, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: evidence file", ErrMediaEvidence)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != s.length {
		return nil, fmt.Errorf("%w: evidence file", ErrMediaEvidence)
	}
	data, err := io.ReadAll(file)
	if err != nil || int64(len(data)) != s.length || sha256.Sum256(data) != s.digest {
		return nil, fmt.Errorf("%w: evidence digest", ErrMediaEvidence)
	}
	return data, nil
}

// Stat checks the private file identity and length without reading its bytes.
func (s *PrivateSource) Stat() (int64, error) {
	if s == nil || s.dir == nil {
		return 0, fmt.Errorf("%w: source is nil", ErrMediaEvidence)
	}
	info, err := s.dir.Stat(s.name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != s.length {
		return 0, fmt.Errorf("%w: evidence file", ErrMediaEvidence)
	}
	return s.length, nil
}

func (s *PrivateSource) discard() {
	if s != nil && s.dir != nil {
		_ = s.dir.Remove(s.name)
	}
}

func (s *PrivateSource) String() string {
	return "wirefixture.PrivateSource{redacted}"
}

func (s *PrivateSource) GoString() string {
	return s.String()
}

type PrivateMediaResolver struct {
	evidence map[MediaLocation]*PrivateSource
}

func NewPrivateMediaResolver(evidence []MediaEvidence) (*PrivateMediaResolver, error) {
	resolver := &PrivateMediaResolver{evidence: make(map[MediaLocation]*PrivateSource, len(evidence))}
	for _, item := range evidence {
		if item.Source == nil || item.Location.Sequence <= 0 || item.Location.Part < -1 {
			return nil, fmt.Errorf("%w: invalid evidence location", ErrMediaEvidence)
		}
		if _, exists := resolver.evidence[item.Location]; exists {
			return nil, fmt.Errorf("%w: duplicate evidence location", ErrMediaEvidence)
		}
		resolver.evidence[item.Location] = item.Source
	}
	return resolver, nil
}

func (r *PrivateMediaResolver) resolve(location MediaLocation, media Media) ([]byte, error) {
	return r.Resolve(location, media)
}

func (r *PrivateMediaResolver) checkLength(location MediaLocation, media Media) error {
	if r == nil {
		return ErrMediaEvidence
	}
	if media.Recipe != "" || media.Substituted {
		return fmt.Errorf("%w: private resolver requires captured media", ErrMediaResolver)
	}
	source, ok := r.evidence[location]
	if !ok || source == nil {
		return fmt.Errorf("%w: no private evidence", ErrMediaEvidence)
	}
	if media.MediaType != source.mediaType || media.Length != source.length {
		return fmt.Errorf("%w: private media descriptor", ErrMediaMismatch)
	}
	_, err := source.Stat()
	return err
}

func (r *PrivateMediaResolver) Resolve(location MediaLocation, media Media) ([]byte, error) {
	if r == nil {
		return nil, ErrMediaEvidence
	}
	if media.Recipe != "" || media.Substituted {
		return nil, fmt.Errorf("%w: private resolver requires captured media", ErrMediaResolver)
	}
	source, ok := r.evidence[location]
	if !ok || source == nil {
		return nil, fmt.Errorf("%w: no private evidence", ErrMediaEvidence)
	}
	if media.MediaType != source.mediaType || media.Length != source.length {
		return nil, fmt.Errorf("%w: private media descriptor", ErrMediaMismatch)
	}
	data, err := source.Bytes()
	if err != nil {
		return nil, err
	}
	return data, nil
}
