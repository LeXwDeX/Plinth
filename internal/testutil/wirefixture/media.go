package wirefixture

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

var supportedMediaTypes = map[string]bool{
	"application/mtl":                                        true,
	"application/octet-stream":                               true,
	"application/sla":                                        true,
	"application/vnd.autodesk.fbx":                           true,
	"application/vnd.ms-package.3dmanufacturing-3dmodel+xml": true,
	"application/x-3mf":                                      true,
	"application/x-fbx":                                      true,
	"application/x-obj":                                      true,
	"application/x-tgif":                                     true,
	"application/x-usdz":                                     true,
	"application/zip":                                        true,
	"image/jpeg":                                             true,
	"image/png":                                              true,
	"image/webp":                                             true,
	"model/3mf":                                              true,
	"model/fbx":                                              true,
	"model/gltf+json":                                        true,
	"model/gltf-binary":                                      true,
	"model/mtl":                                              true,
	"model/obj":                                              true,
	"model/stl":                                              true,
	"model/vnd.usdz+zip":                                     true,
	"text/plain":                                             true,
}

// Media is a public descriptor. Captured evidence has an empty Recipe;
// exported fixtures must use a fixed synthetic recipe and mark it substituted.
// No path, original file name, digest or encoded bytes belong here.
type Media struct {
	MediaType   string `json:"media_type"`
	Length      int64  `json:"length"`
	Recipe      string `json:"recipe,omitempty"`
	Substituted bool   `json:"substituted,omitempty"`
}

// MultipartPart preserves order and duplicate names. A part is either a form
// value or a file descriptor, never both. Original file names are discarded.
type MultipartPart struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	Media *Media `json:"media,omitempty"`
}

type Multipart struct {
	Parts []MultipartPart `json:"parts"`
}

func cloneMedia(media *Media) *Media {
	if media == nil {
		return nil
	}
	cloned := *media
	return &cloned
}

func cloneMultipart(value *Multipart) *Multipart {
	if value == nil {
		return nil
	}
	cloned := &Multipart{Parts: make([]MultipartPart, len(value.Parts))}
	for i, part := range value.Parts {
		cloned.Parts[i] = MultipartPart{Name: part.Name, Value: part.Value, Media: cloneMedia(part.Media)}
	}
	return cloned
}

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

type recipeSpec struct {
	mediaType string
	length    int64
	digest    [sha256.Size]byte
	generate  func() ([]byte, error)
}

func generatePNG() ([]byte, error) {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func generateGLB() ([]byte, error) {
	document := []byte(`{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{"mesh":0}],"meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],"buffers":[{"byteLength":36}],"bufferViews":[{"buffer":0,"byteOffset":0,"byteLength":36}],"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3","max":[1,1,0],"min":[0,0,0]}]}`)
	for len(document)%4 != 0 {
		document = append(document, ' ')
	}
	binaryChunk := make([]byte, 36)
	vertices := []float32{0, 0, 0, 1, 0, 0, 0, 1, 0}
	for i, vertex := range vertices {
		binary.LittleEndian.PutUint32(binaryChunk[i*4:], math.Float32bits(vertex))
	}
	total := 12 + 8 + len(document) + 8 + len(binaryChunk)
	out := make([]byte, 0, total)
	out = append(out, "glTF"...)
	out = binary.LittleEndian.AppendUint32(out, 2)
	out = binary.LittleEndian.AppendUint32(out, uint32(total))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(document)))
	out = binary.LittleEndian.AppendUint32(out, 0x4e4f534a)
	out = append(out, document...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(binaryChunk)))
	out = binary.LittleEndian.AppendUint32(out, 0x004e4942)
	out = append(out, binaryChunk...)
	if len(out) != total {
		return nil, fmt.Errorf("%w: GLB length", ErrMediaMismatch)
	}
	return out, nil
}

func generateJPEG() ([]byte, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func generateOBJ() ([]byte, error) {
	return []byte("o fixture\nv 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n"), nil
}

func generateSTL() ([]byte, error) {
	return []byte("solid fixture\nfacet normal 0 0 1\nouter loop\nvertex 0 0 0\nvertex 1 0 0\nvertex 0 1 0\nendloop\nendfacet\nendsolid fixture\n"), nil
}

func generateGLTF() ([]byte, error) {
	return []byte(`{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{"mesh":0}],"meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],"buffers":[{"byteLength":36}],"bufferViews":[{"buffer":0,"byteOffset":0,"byteLength":36}],"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3","max":[1,1,0],"min":[0,0,0]}]}`), nil
}

func generateFBX() ([]byte, error) {
	out := []byte("Kaydara FBX Binary  \x00\x1a\x00")
	return binary.LittleEndian.AppendUint32(out, 7400), nil
}

func generateEmptyZIP() ([]byte, error) {
	return []byte{'P', 'K', 5, 6, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, nil
}

func generateText() ([]byte, error) {
	return []byte("fixture\n"), nil
}

func generateWebP() ([]byte, error) {
	return hex.DecodeString("524946461c000000574542505650384c0f0000002f000000000710fd8ffe0722a2ff0100")
}

func registerSynthetic(name, mediaType string, generate func() ([]byte, error)) {
	data, err := generate()
	if err != nil {
		panic("wirefixture: synthetic recipe generation: " + name)
	}
	syntheticRecipes[name] = recipeSpec{mediaType: mediaType, length: int64(len(data)), digest: sha256.Sum256(data), generate: generate}
}

func init() {
	registerSynthetic("jpeg-1x1", "image/jpeg", generateJPEG)
	registerSynthetic("obj-triangle", "model/obj", generateOBJ)
	registerSynthetic("stl-triangle", "model/stl", generateSTL)
	registerSynthetic("gltf-json", "model/gltf+json", generateGLTF)
	registerSynthetic("fbx-triangle", "model/fbx", generateFBX)
	registerSynthetic("usdz-empty", "model/vnd.usdz+zip", generateEmptyZIP)
	registerSynthetic("3mf-empty", "model/3mf", generateEmptyZIP)
	registerSynthetic("zip-empty", "application/zip", generateEmptyZIP)
	registerSynthetic("octet-empty", "application/octet-stream", generateEmptyZIP)
	registerSynthetic("text-plain", "text/plain", generateText)
	registerSynthetic("webp-1x1", "image/webp", generateWebP)
}

// Recipe version 1: lengths and SHA-256 digests pin generator output. Changing
// a generator requires a new recipe name (or an explicit version bump) and
// matching updates here and in the recipe self-test.
var syntheticRecipes = map[string]recipeSpec{
	"png-1x1": {
		mediaType: "image/png",
		length:    75,
		digest: [sha256.Size]byte{
			0x2e, 0x39, 0xf0, 0x89, 0x59, 0x21, 0x80, 0x12, 0x43, 0x8d, 0x1b, 0x58, 0xa0, 0x5b, 0x73, 0xfe,
			0x32, 0x6f, 0x02, 0x82, 0x37, 0x32, 0xd5, 0x20, 0x1c, 0xb3, 0x30, 0x22, 0x58, 0x69, 0xbf, 0xe0,
		},
		generate: generatePNG,
	},
	"glb-triangle": {
		mediaType: "model/gltf-binary",
		length:    400,
		digest: [sha256.Size]byte{
			0x85, 0x77, 0x0b, 0x49, 0xba, 0x58, 0xfa, 0x2c, 0xde, 0xf5, 0x80, 0x19, 0xc0, 0x99, 0x43, 0x87,
			0x1c, 0xbb, 0x88, 0x4d, 0xd2, 0x1c, 0xd4, 0xb9, 0x3e, 0xa2, 0x1b, 0x89, 0xd5, 0x77, 0x42, 0x6c,
		},
		generate: generateGLB,
	},
}

// SyntheticMedia returns a derived descriptor and freshly generated bytes.
// The bytes are generated at runtime; no original capture is stored.
func SyntheticMedia(recipe string) (Media, []byte, error) {
	spec, ok := syntheticRecipes[recipe]
	if !ok {
		return Media{}, nil, fmt.Errorf("%w: unknown synthetic recipe", ErrMediaResolver)
	}
	data, err := spec.generate()
	if err != nil {
		return Media{}, nil, fmt.Errorf("%w: synthetic recipe generation", ErrMediaResolver)
	}
	if int64(len(data)) != spec.length || sha256.Sum256(data) != spec.digest {
		return Media{}, nil, fmt.Errorf("%w: synthetic recipe output changed", ErrMediaMismatch)
	}
	return Media{MediaType: spec.mediaType, Length: spec.length, Recipe: recipe, Substituted: true}, data, nil
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
	if s == nil || !supportedMediaTypes[mediaType] {
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

func parseMultipart(raw []byte, contentType string, policy Policy) (*Multipart, map[int][]byte, error) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || mediaType != "multipart/form-data" {
		return nil, nil, fmt.Errorf("%w: multipart content type", ErrUnsupportedBody)
	}
	boundary := params["boundary"]
	if boundary == "" || len(boundary) > 70 || strings.ContainsAny(boundary, "\r\n\"") {
		return nil, nil, fmt.Errorf("%w: multipart boundary", ErrUnsupportedBody)
	}
	first := []byte("--" + boundary)
	if !bytes.HasPrefix(raw, first) {
		return nil, nil, fmt.Errorf("%w: multipart preamble", ErrUnsupportedBody)
	}
	after := raw[len(first):]
	switch {
	case bytes.HasPrefix(after, []byte("\r\n")):
		// Regular first delimiter.
	case bytes.HasPrefix(after, []byte("--")):
		// Close delimiter. Any trailing bytes after it must be CRLF; the
		// trailing-junk check below rejects payload following the close.
		if len(after) > 2 && !bytes.HasPrefix(after[2:], []byte("\r\n")) {
			return nil, nil, fmt.Errorf("%w: multipart preamble", ErrUnsupportedBody)
		}
	default:
		// A longer token such as --boundaryX or illegal transport padding is
		// a preamble, not this boundary.
		return nil, nil, fmt.Errorf("%w: multipart preamble", ErrUnsupportedBody)
	}
	trimmed := bytes.TrimRight(raw, "\r\n")
	if !bytes.HasSuffix(trimmed, []byte("--"+boundary+"--")) {
		return nil, nil, fmt.Errorf("%w: multipart trailer", ErrUnsupportedBody)
	}
	if err := validateMultipartEpilogue(raw, boundary); err != nil {
		return nil, nil, err
	}

	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	parsed := &Multipart{}
	files := map[int][]byte{}
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: multipart body", ErrUnsupportedBody)
		}
		name, isFile, err := partDisposition(part.Header)
		if err != nil {
			_ = part.Close()
			return nil, nil, err
		}
		if !identifierRE.MatchString(name) || !policy.Keys[name] {
			_ = part.Close()
			return nil, nil, fmt.Errorf("%w: multipart part name %q", ErrUnknownKey, name)
		}
		if err := checkMultipartHeaders(part.Header); err != nil {
			_ = part.Close()
			return nil, nil, err
		}
		partType := baseMedia(part.Header.Get("Content-Type"))
		if strings.HasPrefix(partType, "multipart/") {
			_ = part.Close()
			return nil, nil, fmt.Errorf("%w: nested multipart part", ErrUnsupportedBody)
		}
		if isFile {
			if !supportedMediaTypes[partType] {
				_ = part.Close()
				return nil, nil, fmt.Errorf("%w: multipart file type", ErrUnsupportedBody)
			}
			data, err := readPart(part, policy.limit())
			_ = part.Close()
			if err != nil || len(data) == 0 {
				return nil, nil, fmt.Errorf("%w: multipart file part", ErrUnsupportedBody)
			}
			index := len(parsed.Parts)
			parsed.Parts = append(parsed.Parts, MultipartPart{Name: name,
				Media: &Media{MediaType: partType, Length: int64(len(data))}})
			files[index] = data
			continue
		}
		if partType != "" {
			_ = part.Close()
			return nil, nil, fmt.Errorf("%w: multipart field content type", ErrUnsupportedBody)
		}
		data, err := readPart(part, policy.limit())
		_ = part.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: multipart field", ErrUnsupportedBody)
		}
		value := string(data)
		if value != "" {
			if err := checkText(value, false); err != nil {
				return nil, nil, err
			}
			clean, err := policy.resolve(value, 0)
			if err != nil {
				return nil, nil, err
			}
			value = clean
		}
		parsed.Parts = append(parsed.Parts, MultipartPart{Name: name, Value: value})
	}
	if len(parsed.Parts) == 0 {
		return nil, nil, fmt.Errorf("%w: empty multipart", ErrUnsupportedBody)
	}
	return parsed, files, nil
}

func validateMultipartEpilogue(raw []byte, boundary string) error {
	closeMarker := []byte("--" + boundary + "--")
	var tail []byte
	if bytes.HasPrefix(raw, closeMarker) {
		tail = raw[len(closeMarker):]
	} else {
		marker := append([]byte("\r\n"), closeMarker...)
		index := bytes.Index(raw, marker)
		if index < 0 {
			return fmt.Errorf("%w: multipart trailer", ErrUnsupportedBody)
		}
		tail = raw[index+len(marker):]
	}
	for len(tail) > 0 {
		if !bytes.HasPrefix(tail, []byte("\r\n")) {
			return fmt.Errorf("%w: multipart epilogue", ErrUnsupportedBody)
		}
		tail = tail[2:]
	}
	return nil
}

func partDisposition(header textproto.MIMEHeader) (string, bool, error) {
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil || mediaType != "form-data" {
		return "", false, fmt.Errorf("%w: multipart disposition", ErrUnsupportedBody)
	}
	for key := range params {
		if key != "name" && key != "filename" {
			return "", false, fmt.Errorf("%w: multipart disposition parameter", ErrUnsupportedBody)
		}
	}
	name := params["name"]
	if name == "" {
		return "", false, fmt.Errorf("%w: multipart part name", ErrUnsupportedBody)
	}
	_, isFile := params["filename"]
	return name, isFile, nil
}

func checkMultipartHeaders(header textproto.MIMEHeader) error {
	for key := range header {
		switch strings.ToLower(key) {
		case "content-disposition", "content-type", "content-transfer-encoding":
		default:
			return fmt.Errorf("%w: multipart header", ErrUnsupportedBody)
		}
	}
	if encoding := strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))); encoding != "" {
		if encoding != "binary" && encoding != "8bit" {
			return fmt.Errorf("%w: multipart transfer encoding", ErrUnsupportedBody)
		}
	}
	return nil
}

func readPart(part *multipart.Part, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: multipart part", ErrUnsupportedBody)
	}
	return data, nil
}
