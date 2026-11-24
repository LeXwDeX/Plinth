package mattingapiv1

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// testPicture is a test picture: the name it is uploaded under and its pixel size.
type testPicture struct {
	name          string
	width, height int
}

// encodePicture encodes a plain opaque picture in the format its name says.
func encodePicture(t *testing.T, picture testPicture) ([]byte, string) {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, picture.width, picture.height))
	for y := 0; y < picture.height; y++ {
		for x := 0; x < picture.width; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: 200, G: 60, B: 60, A: 255})
		}
	}
	var encoded bytes.Buffer
	if len(picture.name) > 4 && picture.name[len(picture.name)-4:] == ".jpg" {
		if err := jpeg.Encode(&encoded, canvas, nil); err != nil {
			t.Fatal(err)
		}
		return encoded.Bytes(), "image/jpeg"
	}
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes(), "image/png"
}

// picturesForm sends one real picture per entry, so the fake can read its size.
func picturesForm(t *testing.T, pipelineJSON string, pictures ...testPicture) testkit.Body {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("pipeline_params_json", pipelineJSON)
	for _, picture := range pictures {
		data, mediaType := encodePicture(t, picture)
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="files"; filename="`+picture.name+`"`)
		header.Set("Content-Type", mediaType)
		part, _ := writer.CreatePart(header)
		_, _ = part.Write(data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}

// finishedBatch creates a batch of the pictures and polls it to SUCCESS; it
// returns the final batch body.
func finishedBatch(t *testing.T, f *testkit.Fixture, pictures ...testPicture) map[string]any {
	t.Helper()
	status, _, created := f.Do("POST", Prefix+"/jobs", key, picturesForm(t, pipeline, pictures...))
	if status != http.StatusOK {
		t.Fatalf("create: %d %v", status, created)
	}
	return pollUntil(f, t, created["batch_id"].(string), "SUCCESS")
}

// downloaded fetches the picture of the index-th task of a finished batch.
func downloaded(t *testing.T, f *testkit.Fixture, final map[string]any, index int) (header http.Header, data []byte) {
	t.Helper()
	id := testkit.Field(t, final, "tasks", index, "task_id").(string)
	status, header, data := f.Raw("GET", Prefix+"/tasks/"+id+"/download", key, nil)
	if status != http.StatusOK {
		t.Fatalf("download of task %d = %d", index, status)
	}
	return header, data
}

// fileOfSizeForm sends one file of exactly n bytes of filler.
func fileOfSizeForm(t *testing.T, name string, n int) testkit.Body {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("pipeline_params_json", pipeline)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="files"; filename="`+name+`"`)
	header.Set("Content-Type", "image/png")
	part, _ := writer.CreatePart(header)
	_, _ = part.Write(bytes.Repeat([]byte{'x'}, n))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}
