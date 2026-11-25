package fakeupstream

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Picture weight (#629). A fake picture is drawn at the requested pixel size
// but is a flat picture of a few hundred kilobytes, where a real 4K answer is
// megabytes (the PROD sample of #629: a 3072x5504 front view over the
// 14,625,000-byte reference limit of the Gemini image-to-image Offering). A
// generated picture used as a later input therefore never crossed an
// Offering's max_bytes on a fake stack, and the input fitting before freezing
// (#276) never ran. The selectors below make a picture at least that many
// bytes by padding it with a chunk every decoder skips, so the pixels, the
// size and the format stay those of the fake picture and only the weight
// changes. Nothing is padded by default.
//
// The first selector present wins:
//
//   - the header X-Fake-Response-Bytes: <n> (with X-Fake-Response-Filler:
//     noise for incompressible filler), for direct tests;
//   - the API key fake-response-bytes-<n>[-noise], for a stack whose requests
//     cannot carry extra headers;
//   - the prompt marker [[fake:picture-bytes:<n>]] or
//     [[fake:picture-bytes:<n>:noise]], for a stack whose only input is a chat
//     message (a deployed fake stack): the marker travels inside the prompt
//     Plinth sends;
//   - the stack-wide weight set with PUT PictureWeightPath, which applies to
//     every picture of at least MinPixels pixels until DELETE resets it. It
//     reaches pictures whose prompt Plinth writes itself (a three-view's front
//     view), which no prompt marker can.
//
// The weight is capped at MaxPictureBytes so no request can ask the fake for
// an arbitrary allocation, and a padded picture is cached (bounded), so the
// same request does not pad again.
const (
	PictureBytesHeader    = "X-Fake-Response-Bytes"
	PictureFillerHeader   = "X-Fake-Response-Filler"
	PictureBytesKeyPrefix = KeyPrefix + "response-bytes-"
	PictureBytesMarker    = "[[fake:picture-bytes:"
	PictureWeightPath     = "/fake/control/picture-weight"
	MaxPictureBytes       = MaxPaddedJPEGBytes
)

// Filler is what pads a picture: FillerText (the default) is a repeating
// text that compresses to almost nothing, FillerNoise a deterministic
// byte stream no general compressor shrinks.
type Filler string

const (
	FillerText  Filler = "text"
	FillerNoise Filler = "noise"
)

// PictureWeight asks for pictures of at least Bytes bytes. MinPixels only
// applies to the stack-wide weight: smaller pictures keep their natural size,
// the way a real small picture is light.
type PictureWeight struct {
	Bytes     int    `json:"bytes"`
	MinPixels int64  `json:"min_pixels,omitempty"`
	Filler    Filler `json:"filler,omitempty"`
}

func (w PictureWeight) validate() error {
	if w.Bytes < 0 || w.Bytes > MaxPictureBytes {
		return fmt.Errorf("bytes must be 0..%d", MaxPictureBytes)
	}
	if w.MinPixels < 0 {
		return fmt.Errorf("min_pixels must not be negative")
	}
	if w.Filler != "" && w.Filler != FillerText && w.Filler != FillerNoise {
		return fmt.Errorf("filler must be %q or %q", FillerText, FillerNoise)
	}
	return nil
}

// SetPictureWeight sets the stack-wide weight; the zero value resets it.
func (e *Env) SetPictureWeight(weight PictureWeight) error {
	if err := weight.validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pictureWeight = weight
	return nil
}

// StackPictureWeight is the stack-wide weight.
func (e *Env) StackPictureWeight() PictureWeight {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pictureWeight
}

// PictureWeightOf is the weight one picture of width x height asks for:
// prompt is the request's prompt text (any text the marker may appear in).
// The zero weight keeps the picture as drawn.
func (e *Env) PictureWeightOf(r *http.Request, prompt string, width, height int) PictureWeight {
	if raw := strings.TrimSpace(r.Header.Get(PictureBytesHeader)); raw != "" {
		return parseWeight(raw, Filler(strings.TrimSpace(r.Header.Get(PictureFillerHeader))))
	}
	if raw, found := strings.CutPrefix(BearerKey(r), PictureBytesKeyPrefix); found {
		if number, isNoise := strings.CutSuffix(raw, "-"+string(FillerNoise)); isNoise {
			return parseWeight(number, FillerNoise)
		}
		return parseWeight(raw, FillerText)
	}
	if _, rest, found := strings.Cut(prompt, PictureBytesMarker); found {
		if body, _, closed := strings.Cut(rest, "]]"); closed {
			number, filler, _ := strings.Cut(body, ":")
			return parseWeight(number, Filler(filler))
		}
	}
	if stack := e.StackPictureWeight(); stack.Bytes > 0 && int64(width)*int64(height) >= stack.MinPixels {
		return PictureWeight{Bytes: stack.Bytes, Filler: stack.Filler}
	}
	return PictureWeight{}
}

// parseWeight reads a byte count; a malformed or negative one is no weight.
func parseWeight(raw string, filler Filler) PictureWeight {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < 0 {
		return PictureWeight{}
	}
	if filler != FillerNoise {
		filler = FillerText
	}
	return PictureWeight{Bytes: min(parsed, MaxPictureBytes), Filler: filler}
}

type weightedKey struct {
	spec   PictureSpec
	bytes  int
	filler Filler
}

// weightedCache keeps padded pictures up to MaxPictureBytes in all; when a new
// one does not fit, the cache starts over. The returned bytes are shared like
// Picture's.
var weightedCache struct {
	sync.Mutex
	files map[weightedKey]File
	total int
}

// WeightedPicture is Picture padded to weight (see PictureWeightOf); a weight
// at or below the drawn size returns the picture unchanged.
func WeightedPicture(spec PictureSpec, weight PictureWeight) (File, error) {
	file, err := Picture(spec)
	if err != nil || weight.Bytes <= len(file.Data) {
		return file, err
	}
	key := weightedKey{spec: spec, bytes: weight.Bytes, filler: weight.Filler}
	weightedCache.Lock()
	defer weightedCache.Unlock()
	if cached, ok := weightedCache.files[key]; ok {
		return cached, nil
	}
	padded := PadFileWith(file, weight.Bytes, weight.Filler)
	if weightedCache.files == nil || weightedCache.total+len(padded.Data) > MaxPictureBytes {
		weightedCache.files, weightedCache.total = map[weightedKey]File{}, 0
	}
	if len(padded.Data) <= MaxPictureBytes {
		weightedCache.files[key] = padded
		weightedCache.total += len(padded.Data)
	}
	return padded, nil
}
