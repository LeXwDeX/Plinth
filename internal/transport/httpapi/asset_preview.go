package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
)

// AssetPreviews makes and keeps the downscaled copies of pictures (#290); see assetdelivery.PreviewStore.
type AssetPreviews interface {
	Accepts(mediaType string, size int64) bool
	Cached(digest string) ([]byte, bool)
	Preview(ctx context.Context, digest string, mediaType string, original io.Reader) ([]byte, []byte, error)
	Tag(digest string) string
	MediaType() string
}

// previewSource is an owned picture the caller has already authorized.
type previewSource struct {
	Digest    string
	MediaType string
	Size      int64
	Body      io.Reader
}

// wantsAssetPreview: the request names the Schema's preview query value and this picture can have one.
// Everything else (models, unknown or oversized pictures) is answered with the original as before.
func (s *Server) wantsAssetPreview(request *http.Request, mediaType string, size int64) bool {
	preview := s.application.Runtime.AssetDelivery.AssetPreview
	return s.assetPreviews != nil && preview.Query != "" &&
		request.URL.Query().Get(preview.Query) == preview.RequestValue &&
		s.assetPreviews.Accepts(mediaType, size)
}

// serveAssetPreview answers with the preview of an authorized picture (task output or input asset). When no
// preview can be made it writes nothing and returns the bytes it read from the body, so the caller serves the
// original from those and the rest of the body. `audit` records the access when bytes are sent.
//
// A preview is cached by the browser like a role thumbnail (#225): private, short max-age, varying by the
// signed-in session, revalidated by an ETag derived from the original's digest and the preview size. The
// validator never skips authorization: the caller has checked ownership before calling.
func (s *Server) serveAssetPreview(
	writer http.ResponseWriter,
	request *http.Request,
	source previewSource,
	audit func(),
) (bool, []byte) {
	previews := s.assetPreviews
	etag := `"` + previews.Tag(source.Digest) + `"`
	setPreviewCaching := func() {
		writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.ThumbnailCacheControl)
		writer.Header().Set("Vary", "Cookie, Authorization")
		writer.Header().Set("ETag", etag)
	}
	if etagMatches(request.Header.Get("If-None-Match"), etag) {
		setPreviewCaching()
		writer.WriteHeader(http.StatusNotModified)
		return true, nil
	}
	preview, cached := previews.Cached(source.Digest)
	var read []byte
	if !cached {
		var err error
		preview, read, err = previews.Preview(request.Context(), source.Digest, source.MediaType, source.Body)
		switch {
		case errors.Is(err, assetdelivery.ErrPreviewNotStored):
			noteCause(writer, err)
		case request.Context().Err() != nil:
			// The client went away; nothing is left to answer.
			return true, nil
		case err != nil:
			if !errors.Is(err, assetdelivery.ErrPreviewUnsupported) {
				noteCause(writer, err)
			}
			return false, read
		}
	}
	audit()
	setPreviewCaching()
	writer.Header().Set("Content-Type", previews.MediaType())
	writer.Header().Set("Content-Length", strconv.Itoa(len(preview)))
	writer.Header().Set("Content-Disposition", "inline")
	writer.Header().Set("Content-Security-Policy", "sandbox")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(preview)
	return true, nil
}
