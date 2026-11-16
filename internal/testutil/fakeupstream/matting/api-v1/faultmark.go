package mattingapiv1

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
)

// FileFaultMarker starts the list of faults a picture carries in its own bytes
// (#301).
//
// A deployed Plinth stack reaches the fake through its own Adapter: the request
// has no test header, the key is fixed, and the upload filenames are rewritten
// (input_<n>.<ext>). Only the bytes of the uploaded file arrive as they were
// stored (the Offering's inputs are kept as uploaded), so the fake also reads
// the faults of a file from the head of the file itself:
//
//	fake-file-faults:<fault>[,<fault>...];
//
// where each fault is one of the words of FileFaultsHeader (rejected, failed,
// gpu_oom, renamed) and the list ends at the first ";". The file gets those
// faults wherever it sits in the batch, added to what the request header asks
// for that position. In a PNG the text belongs in a tEXt chunk (MarkPNG writes
// it); in a JPEG it can be a COM segment. The fake looks at the first headerRead
// bytes only, which is also all it reads for the size of the picture.
//
// Why the file and not a control route: a stack in which only Plinth's own port
// is reachable (the browser and the smoke scripts never talk to the fake) can
// still choose a fault, nothing is shared between requests (so concurrent
// batches cannot consume or borrow each other's faults and a marker outlives
// nothing but its own picture), and a picture that carries no marker is never
// touched. The Adapter has no branch for it: the marker is only ever read here,
// and a real service would see an ordinary picture with a comment.
const FileFaultMarker = "fake-file-faults:"

// fileFaultMarkerEnd closes the list of a marker. The list needs a closing mark
// because what follows the text in a container is not text: the CRC of a PNG
// text chunk could otherwise read as the end of the last word.
const fileFaultMarkerEnd = ';'

// markerReach bounds how far from the start of a marker its closing mark is
// looked for; the longest list (every fault word once) is about 30 bytes.
const markerReach = 96

// FaultMark is the marker text that asks for the given faults.
func FaultMark(faults ...string) string {
	return FileFaultMarker + strings.Join(faults, ",") + string(fileFaultMarkerEnd)
}

// markedFaults reads the fault words of every marker in the head of a file.
// A marker whose list holds anything but fault-word bytes, or is not closed
// within markerReach, asks for nothing and the search goes on after it.
func markedFaults(head []byte) []string {
	var words []string
	for {
		at := bytes.Index(head, []byte(FileFaultMarker))
		if at < 0 {
			return words
		}
		head = head[at+len(FileFaultMarker):]
		reach := head[:min(len(head), markerReach)]
		end := 0
		for end < len(reach) && isFaultListByte(reach[end]) {
			end++
		}
		if end == len(reach) || reach[end] != fileFaultMarkerEnd {
			continue
		}
		for _, word := range strings.Split(string(reach[:end]), ",") {
			if word = strings.TrimSpace(word); word != "" {
				words = append(words, word)
			}
		}
		head = head[end+1:]
	}
}

// isFaultListByte reports whether b can be part of a fault list: the lowercase
// letters and underscores of the fault words, the comma between them and a
// space.
func isFaultListByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b == '_' || b == ',' || b == ' '
}

// pngSignature starts every PNG file.
const pngSignature = "\x89PNG\r\n\x1a\n"

// MarkPNG returns the PNG with a text chunk that asks for the given faults (see
// FileFaultMarker): written right after the header chunk, so it lies within the
// head the fake reads, and with a valid CRC, so every decoder still reads the
// picture. The faults must be words the fake knows.
func MarkPNG(picture []byte, faults ...string) ([]byte, error) {
	if len(faults) == 0 {
		return nil, errors.New("mattingapiv1: a fault marker needs at least one fault")
	}
	var known fileFault
	for _, word := range faults {
		if !known.add(word) {
			return nil, fmt.Errorf("mattingapiv1: %q is not a file fault", word)
		}
	}
	// The header chunk: length (13), type, 13 bytes of data, CRC.
	headerEnd := len(pngSignature) + 4 + len("IHDR") + 13 + 4
	if len(picture) < headerEnd || !bytes.HasPrefix(picture, []byte(pngSignature)) ||
		binary.BigEndian.Uint32(picture[len(pngSignature):]) != 13 ||
		string(picture[len(pngSignature)+4:len(pngSignature)+8]) != "IHDR" {
		return nil, errors.New("mattingapiv1: not a PNG picture")
	}
	text := "Comment\x00" + FaultMark(faults...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(text)))
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, text...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	marked := make([]byte, 0, len(picture)+len(chunk))
	marked = append(marked, picture[:headerEnd]...)
	marked = append(marked, chunk...)
	return append(marked, picture[headerEnd:]...), nil
}
