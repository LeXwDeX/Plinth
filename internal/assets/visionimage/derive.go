// Package visionimage makes the copy of a picture that the LLM's vision
// context reads when the picture itself is over the inline byte limit (#292).
//
// A person attaches a photo so the Agent can look at it. Before #292 such a
// photo reached the model as the data URI the browser had read, at any size;
// once composer pictures became input assets, every platform picture over
// chat.context.vision_inline_max_bytes was left out of vision and only named
// by its text label. Here the picture is decoded, scaled so its long side fits
// a Schema step, and encoded until it fits the limit, so the model still sees
// it. The copy is for the model's eyes only: generation references sent to a
// Provider never go through this package and keep the no-resize fitting of
// #276 (internal/assets/imagefit).
package visionimage

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decode GIF sources
	"image/jpeg"
	"image/png"
	"regexp"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode WebP sources

	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/imaging"
)

// ErrUnfit means no copy within the limit could be made (not a decodable
// picture, over the decode bounds, or still too large at the smallest step).
// The caller then names the picture by its text label, as before #292.
var ErrUnfit = errors.New("visionimage: no vision copy within the limit")

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Copy is a vision copy of a picture.
type Copy struct {
	Body      []byte
	MediaType string
	Width     int
	Height    int
}

// Deriver makes vision copies and keeps the recent ones in memory, keyed by
// the source's SHA-256 and the byte limit, so a picture that stays in the
// recent turns is encoded once. The cache lives as long as the process.
type Deriver struct {
	policy      catalog.ChatVisionDerivativeSchema
	background  color.RGBA
	fingerprint string
	slots       chan struct{}

	mutex   sync.Mutex
	entries map[string]*list.Element
	order   *list.List
	bytes   int64
	// made counts the copies encoded (not served from the cache), for tests.
	made int
}

type cacheEntry struct {
	key  string
	copy Copy
}

// New checks the policy (as the catalog validates it) and returns a Deriver.
func New(policy catalog.ChatVisionDerivativeSchema) (*Deriver, error) {
	background, err := imagefit.ParseHexColor(strings.ToLower(policy.FlattenColor))
	if err != nil || len(policy.LongSideSteps) == 0 || len(policy.JPEGQualities) == 0 ||
		policy.MaximumInputBytes <= 0 || policy.MaximumPixels <= 0 ||
		policy.DecodeConcurrency <= 0 || policy.CacheMaximumBytes <= 0 {
		return nil, errors.New("visionimage: invalid vision derivative policy")
	}
	return &Deriver{
		policy:      policy,
		background:  background,
		fingerprint: fmt.Sprintf("%v|%v|%s", policy.LongSideSteps, policy.JPEGQualities, strings.ToLower(policy.FlattenColor)),
		slots:       make(chan struct{}, policy.DecodeConcurrency),
		entries:     make(map[string]*list.Element),
		order:       list.New(),
	}, nil
}

// MaximumInputBytes is the largest source the Deriver reads.
func (deriver *Deriver) MaximumInputBytes() int64 { return deriver.policy.MaximumInputBytes }

// Cached returns the copy already made for the source with this SHA-256 under
// this limit, without reading the source.
func (deriver *Deriver) Cached(digest string, maxBytes int64) (Copy, bool) {
	if !sha256Hex.MatchString(digest) {
		return Copy{}, false
	}
	deriver.mutex.Lock()
	defer deriver.mutex.Unlock()
	element, ok := deriver.entries[deriver.key(digest, maxBytes)]
	if !ok {
		return Copy{}, false
	}
	deriver.order.MoveToFront(element)
	return element.Value.(*cacheEntry).copy, true
}

// Derive returns a copy of source within maxBytes. digest is the source's
// recorded SHA-256; when it is not a valid one the bytes are hashed.
func (deriver *Deriver) Derive(ctx context.Context, source []byte, digest string, maxBytes int64) (Copy, error) {
	if maxBytes <= 0 || len(source) == 0 || int64(len(source)) > deriver.policy.MaximumInputBytes {
		return Copy{}, ErrUnfit
	}
	if !sha256Hex.MatchString(digest) {
		sum := sha256.Sum256(source)
		digest = hex.EncodeToString(sum[:])
	}
	if cached, ok := deriver.Cached(digest, maxBytes); ok {
		return cached, nil
	}
	select {
	case deriver.slots <- struct{}{}:
		defer func() { <-deriver.slots }()
	case <-ctx.Done():
		return Copy{}, ctx.Err()
	}
	// Another turn may have made it while this one waited for a slot.
	if cached, ok := deriver.Cached(digest, maxBytes); ok {
		return cached, nil
	}
	made, err := deriver.encode(source, maxBytes)
	if err != nil {
		return Copy{}, err
	}
	deriver.store(deriver.key(digest, maxBytes), made)
	return made, nil
}

func (deriver *Deriver) key(digest string, maxBytes int64) string {
	return fmt.Sprintf("%s|%d|%s", digest, maxBytes, deriver.fingerprint)
}

// encode tries every long-side step, largest first; at each size a transparent
// picture is tried as PNG first (its alpha kept), then every picture as JPEG
// (transparency composited over the Schema colour) at each quality.
func (deriver *Deriver) encode(source []byte, maxBytes int64) (Copy, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(source))
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width) > deriver.policy.MaximumPixels/int64(config.Height) {
		return Copy{}, ErrUnfit
	}
	decoded, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return Copy{}, ErrUnfit
	}
	transparent := !opaque(decoded)
	previousWidth, previousHeight := 0, 0
	for _, side := range deriver.policy.LongSideSteps {
		width, height := imaging.FitWithin(config.Width, config.Height, side)
		if width == previousWidth && height == previousHeight {
			// A picture smaller than this step was already tried at its own size.
			continue
		}
		previousWidth, previousHeight = width, height
		scaled := scale(decoded, width, height)
		if transparent {
			var buffer bytes.Buffer
			encoder := png.Encoder{CompressionLevel: png.BestCompression}
			if err := encoder.Encode(&buffer, scaled); err != nil {
				return Copy{}, err
			}
			if int64(buffer.Len()) <= maxBytes {
				return Copy{Body: buffer.Bytes(), MediaType: "image/png", Width: width, Height: height}, nil
			}
		}
		var lossy image.Image = scaled
		if transparent {
			lossy = imagefit.Flatten(scaled, deriver.background)
		}
		for _, quality := range deriver.policy.JPEGQualities {
			var buffer bytes.Buffer
			if err := jpeg.Encode(&buffer, lossy, &jpeg.Options{Quality: quality}); err != nil {
				return Copy{}, err
			}
			if int64(buffer.Len()) <= maxBytes {
				return Copy{Body: buffer.Bytes(), MediaType: "image/jpeg", Width: width, Height: height}, nil
			}
		}
	}
	return Copy{}, ErrUnfit
}

// scale resamples to width×height; at the source's own size it only converts.
func scale(source image.Image, width, height int) image.Image {
	bounds := source.Bounds()
	if bounds.Dx() == width && bounds.Dy() == height {
		return source
	}
	scaled := image.NewNRGBA(image.Rect(0, 0, width, height))
	// Catmull-Rom widens its kernel when shrinking, like the asset previews (#290).
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), source, bounds, xdraw.Src, nil)
	return scaled
}

func (deriver *Deriver) store(key string, made Copy) {
	size := int64(len(made.Body))
	deriver.mutex.Lock()
	defer deriver.mutex.Unlock()
	deriver.made++
	if size > deriver.policy.CacheMaximumBytes {
		return
	}
	if _, exists := deriver.entries[key]; exists {
		return
	}
	deriver.entries[key] = deriver.order.PushFront(&cacheEntry{key: key, copy: made})
	deriver.bytes += size
	for deriver.bytes > deriver.policy.CacheMaximumBytes {
		oldest := deriver.order.Back()
		entry := oldest.Value.(*cacheEntry)
		deriver.order.Remove(oldest)
		delete(deriver.entries, entry.key)
		deriver.bytes -= int64(len(entry.copy.Body))
	}
}

// opaque reports whether every pixel is fully opaque.
func opaque(decoded image.Image) bool {
	if checker, ok := decoded.(interface{ Opaque() bool }); ok {
		return checker.Opaque()
	}
	bounds := decoded.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, alpha := decoded.At(x, y).RGBA(); alpha != 0xffff {
				return false
			}
		}
	}
	return true
}
