// Package inlinepictures re-encodes the pictures a chat model request carries
// inline (data URIs) into the format the Product Schema declares for them
// (chat.agent.request_pictures). Every picture the Agent may see goes through
// it: the chat layer encodes a picture before the vision budget counts its
// bytes, and the Adapter passes the whole request through it once more before
// the request is encoded, so no picture reaches the wire in another format.
//
// Only JPEG is declared today. A picture with transparency is first
// composited over the declared background colour; a JPEG that needs no EXIF
// rotation is kept byte for byte (re-encoding it would only lose quality); a
// picture that cannot be decoded, or is larger than the declared pixel bound,
// is kept as it is. A picture is never resized.
//
// 请求体大小调查记录（#555，2026-11-24；依据是内部 LLMS 网关的访问日志与代理
// 日志，未做网关侧配置核对）：
//   - 现象：gpt-astra（Responses 接口）在工具调用之后的续跑轮连续返回 502 Bad
//     Gateway；失败请求体约 10.3 MB、12.4 MB，同一会话里最后一次成功的续跑请求
//     约 7.4 MB（耗时约 13 秒）。请求里带着多张原始 PNG。
//   - 网关侧：失败请求在发往上游 WebSocket 的发送阶段约 9 毫秒内即报
//     network_error，不可能是传输耗时，推断为网关 WebSocket 通道自身的单条
//     消息/请求体大小限制；已知界线在 7.4 MB 到 10.3 MB 之间，阈值未经网关确认。
//     网关对 Plinth 回 502。
//   - 排除项：OpenAI 官方确认单个请求 body 上限为 30 MB（2026-11-24 求证），
//     失败请求远低于它，不是上游模型服务的大小限制。
//   - 处置：网关已关闭 WebSocket 通道，需持续观察。Plinth 当前不对请求体大小
//     做上限与降档，仅把内联图片统一转为 JPEG 以减小体积；context.vision.
//     maximum_bytes（16 MiB）是图片字节预算，按转码后字节计算。若再出现，用
//     upstreamhttp.CallLogger 的失败调用行（状态码、上游 request id、响应体前
//     200 字节、请求体大小）再查。
package inlinepictures

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	_ "image/gif" // decode GIF sources
	"image/jpeg"
	_ "image/png" // decode PNG sources
	"strconv"
	"strings"
	"sync"

	_ "golang.org/x/image/bmp"  // decode BMP sources
	_ "golang.org/x/image/webp" // decode WebP sources

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
)

// FormatJPEG is the only format chat.agent.request_pictures declares today.
const FormatJPEG = "jpeg"

// cacheBytes bounds the encoded pictures kept in memory. A request is rebuilt
// every round, so the same picture is asked for again and again; the bound is
// a memory guard, not product configuration.
const cacheBytes int64 = 64 << 20

// Policy is the Schema's chat.agent.request_pictures declaration.
type Policy struct {
	Format     string
	Quality    int
	Background color.RGBA
	// MaximumPixels bounds the pictures that are decoded at all; a larger one
	// is kept as it is.
	MaximumPixels int64
}

// Stats is what one request's pass did to its pictures.
type Stats struct {
	// Images counts the request's picture parts, inline or not.
	Images int
	// OriginalBytes and Bytes are the inline pictures' data URI lengths before
	// and after encoding. A picture the chat layer already encoded counts by
	// the size it had before that, when the encoder still remembers it.
	OriginalBytes int64
	Bytes         int64
	// Encoded counts the pictures this pass re-encoded (cache hits included);
	// Kept the inline pictures left as they were.
	Encoded int
	Kept    int
}

type entry struct {
	output string
}

// Encoder encodes pictures under one Policy and remembers its results. A nil
// Encoder leaves every picture as it is.
type Encoder struct {
	policy Policy

	mutex sync.Mutex
	// cache maps the SHA-256 of an input data URI to its output.
	cache map[string]entry
	// origins maps an output data URI to the length of the input it came
	// from, so a later pass can report the bytes before encoding.
	origins map[string]int64
	order   []string
	size    int64
	// hits counts cache hits, for tests and diagnostics.
	hits int
}

// New returns an Encoder for a validated Policy.
func New(policy Policy) (*Encoder, error) {
	if policy.Format != FormatJPEG {
		return nil, errors.New("inlinepictures: unsupported format " + strconv.Quote(policy.Format))
	}
	if policy.Quality < 1 || policy.Quality > 100 {
		return nil, errors.New("inlinepictures: quality must be between 1 and 100")
	}
	if policy.MaximumPixels <= 0 {
		return nil, errors.New("inlinepictures: maximum pixels must be positive")
	}
	return &Encoder{policy: policy, cache: map[string]entry{}, origins: map[string]int64{}}, nil
}

// CacheHits is how many encodings the cache answered.
func (encoder *Encoder) CacheHits() int {
	if encoder == nil {
		return 0
	}
	encoder.mutex.Lock()
	defer encoder.mutex.Unlock()
	return encoder.hits
}

// URL returns the picture in the declared format: a data URI is re-encoded
// (or kept, see the package comment); any other value is returned unchanged.
func (encoder *Encoder) URL(url string) string {
	output, _ := encoder.encode(url)
	return output
}

// Request returns the request with every inline picture in the declared
// format, and what the pass did. The request's own slices are never changed.
func (encoder *Encoder) Request(request agents.Request) (agents.Request, Stats) {
	var stats Stats
	var messages []agents.Message
	for messageIndex, message := range request.Messages {
		var parts []agents.ContentPart
		for partIndex, part := range message.Parts {
			if part.URL == "" {
				continue
			}
			stats.Images++
			if !inline(part.URL) {
				continue
			}
			output, encoded := encoder.encode(part.URL)
			stats.OriginalBytes += encoder.origin(part.URL)
			stats.Bytes += int64(len(output))
			if encoded {
				stats.Encoded++
			} else {
				stats.Kept++
			}
			if output == part.URL {
				continue
			}
			if parts == nil {
				parts = append([]agents.ContentPart(nil), message.Parts...)
			}
			parts[partIndex].URL = output
		}
		if parts == nil {
			continue
		}
		if messages == nil {
			messages = append([]agents.Message(nil), request.Messages...)
		}
		messages[messageIndex].Parts = parts
	}
	if messages != nil {
		request.Messages = messages
	}
	return request, stats
}

func inline(url string) bool {
	return strings.HasPrefix(strings.ToLower(url), "data:image/")
}

// origin is the length of the input a URL was encoded from, or its own.
func (encoder *Encoder) origin(url string) int64 {
	if encoder != nil {
		encoder.mutex.Lock()
		length, found := encoder.origins[url]
		encoder.mutex.Unlock()
		if found {
			return length
		}
	}
	return int64(len(url))
}

// encode returns the picture's output and whether it was re-encoded.
func (encoder *Encoder) encode(url string) (string, bool) {
	if encoder == nil || !inline(url) {
		return url, false
	}
	digest := sha256.Sum256([]byte(url))
	key := hex.EncodeToString(digest[:])
	encoder.mutex.Lock()
	if cached, found := encoder.cache[key]; found {
		encoder.hits++
		encoder.mutex.Unlock()
		return cached.output, cached.output != url
	}
	encoder.mutex.Unlock()
	output := encoder.transcode(url)
	encoder.remember(key, url, output)
	return output, output != url
}

func (encoder *Encoder) remember(key, input, output string) {
	encoder.mutex.Lock()
	defer encoder.mutex.Unlock()
	if _, found := encoder.cache[key]; found {
		return
	}
	cost := int64(len(output))
	if cost > cacheBytes {
		return
	}
	for encoder.size+cost > cacheBytes && len(encoder.order) > 0 {
		oldest := encoder.order[0]
		encoder.order = encoder.order[1:]
		evicted := encoder.cache[oldest]
		delete(encoder.cache, oldest)
		delete(encoder.origins, evicted.output)
		encoder.size -= int64(len(evicted.output))
	}
	encoder.cache[key] = entry{output: output}
	encoder.order = append(encoder.order, key)
	encoder.size += cost
	if output != input {
		encoder.origins[output] = int64(len(input))
	}
}

// transcode re-encodes one data URI, or returns it unchanged when it needs no
// change or cannot be read.
func (encoder *Encoder) transcode(url string) string {
	comma := strings.IndexByte(url, ',')
	if comma < 0 || !strings.HasSuffix(strings.ToLower(url[:comma]), ";base64") {
		return url
	}
	data, err := base64.StdEncoding.DecodeString(url[comma+1:])
	if err != nil {
		return url
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width)*int64(config.Height) > encoder.policy.MaximumPixels {
		return url
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(data)
		if orientation == 1 {
			// Already JPEG and upright: re-encoding would only lose quality.
			return url
		}
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return url
	}
	picture := imagefit.Flatten(decoded, encoder.policy.Background)
	picture = orient(picture, orientation)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, picture, &jpeg.Options{Quality: encoder.policy.Quality}); err != nil {
		return url
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

// orient applies an EXIF orientation (1–8) to an upright copy.
func orient(source *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return source
	}
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	outWidth, outHeight := width, height
	if orientation >= 5 {
		outWidth, outHeight = height, width
	}
	result := image.NewRGBA(image.Rect(0, 0, outWidth, outHeight))
	for y := 0; y < outHeight; y++ {
		for x := 0; x < outWidth; x++ {
			var sourceX, sourceY int
			switch orientation {
			case 2: // mirrored horizontally
				sourceX, sourceY = width-1-x, y
			case 3: // rotated 180°
				sourceX, sourceY = width-1-x, height-1-y
			case 4: // mirrored vertically
				sourceX, sourceY = x, height-1-y
			case 5: // transposed
				sourceX, sourceY = y, x
			case 6: // to be rotated 90° clockwise
				sourceX, sourceY = y, height-1-x
			case 7: // transversed
				sourceX, sourceY = width-1-y, height-1-x
			case 8: // to be rotated 90° counter-clockwise
				sourceX, sourceY = width-1-y, x
			}
			offset := source.PixOffset(sourceX, sourceY)
			copy(result.Pix[result.PixOffset(x, y):], source.Pix[offset:offset+4])
		}
	}
	return result
}

// jpegOrientation reads the EXIF orientation of a JPEG; 1 when it has none.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	position := 2
	for position+4 <= len(data) {
		if data[position] != 0xFF {
			return 1
		}
		marker := data[position+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan, end of image
			return 1
		}
		length := int(data[position+2])<<8 | int(data[position+3])
		if length < 2 || position+2+length > len(data) {
			return 1
		}
		segment := data[position+4 : position+2+length]
		if marker == 0xE1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			return tiffOrientation(segment[6:])
		}
		position += 2 + length
	}
	return 1
}

// tiffOrientation reads tag 0x0112 of a TIFF header's first IFD.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var read16 func([]byte) int
	var read32 func([]byte) int
	switch string(tiff[:2]) {
	case "II":
		read16 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 }
		read32 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24 }
	case "MM":
		read16 = func(b []byte) int { return int(b[0])<<8 | int(b[1]) }
		read32 = func(b []byte) int { return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3]) }
	default:
		return 1
	}
	offset := read32(tiff[4:8])
	if offset < 8 || offset+2 > len(tiff) {
		return 1
	}
	count := read16(tiff[offset : offset+2])
	for index := 0; index < count; index++ {
		at := offset + 2 + index*12
		if at+12 > len(tiff) {
			return 1
		}
		if read16(tiff[at:at+2]) != 0x0112 {
			continue
		}
		value := read16(tiff[at+8 : at+10])
		if value < 1 || value > 8 {
			return 1
		}
		return value
	}
	return 1
}
