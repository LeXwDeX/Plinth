package upstreamhttp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
)

// #497: a 400 whose error objects to the picture itself (its dimensions, its
// size, its format, the number of pictures) is a bad picture, not a model
// without image input. The body below is the dev-test refusal verbatim: a
// qwen model answered 400 invalid_parameter_error for a 1-pixel picture.
const devTestPictureRefusal = `{"error":{"code":"invalid_parameter_error","param":null,` +
	`"message":"The image length and width do not meet the model restrictions. ` +
	`[height:1 or width:1 must be larger than 32]","type":"invalid_parameter_error"}}`

// #253: a refusal the model or endpoint makes because it takes no picture
// input at all is still the images-rejected class.
const devTestNoImageInput = `{"error":{"code":"invalid_request_error",` +
	`"message":"this model does not support image input","type":"invalid_request_error"}}`

func TestRefusalSeparatesABadPictureFromAModelWithoutImageInput(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		body     string
		rejected bool
		invalid  []string
		param    string
	}{
		{
			name:    "a picture whose dimensions the model refuses",
			status:  400,
			body:    devTestPictureRefusal,
			invalid: []string{"dimensions"},
		},
		{
			name:    "a picture too large for the model",
			status:  400,
			body:    `{"error":{"code":"invalid_parameter_error","message":"The image size exceeds the maximum of 10 MB","type":"invalid_parameter_error"}}`,
			invalid: []string{"size"},
		},
		{
			name:    "a picture format the model refuses",
			status:  400,
			body:    `{"error":{"code":"invalid_parameter_error","message":"The image format is not supported","type":"invalid_parameter_error"}}`,
			invalid: []string{"format"},
		},
		{
			name:    "too many pictures in one request",
			status:  400,
			body:    `{"error":{"code":"invalid_parameter_error","message":"too many images: the maximum number of images is 10","type":"invalid_parameter_error"}}`,
			invalid: []string{"count"},
		},
		{
			name:    "a streamed refusal",
			status:  400,
			body:    "data: " + devTestPictureRefusal,
			invalid: []string{"dimensions"},
		},
		{
			name:     "a model that takes no picture input",
			status:   400,
			body:     devTestNoImageInput,
			rejected: true,
		},
		{
			name:     "415 is the request's media type refused",
			status:   415,
			body:     `{"error":{"message":"model has no VISION capability"}}`,
			rejected: true,
		},
		{
			name:     "a wording that only says image input is unsupported",
			status:   422,
			body:     `{"error":{"message":"Multimodal content not allowed"}}`,
			rejected: true,
		},
		{
			name:   "a request field that has nothing to do with pictures",
			status: 400,
			body:   `{"error":{"code":"invalid_request_error","message":"temperature must be <= 2","param":"temperature","type":"invalid_request_error"}}`,
			param:  "temperature",
		},
		{
			// A tool declaration refused in a request that carried pictures:
			// the Agent's tool names say "image" (refine_image), and the
			// schema's own words ("width", "format") are the picture
			// vocabulary. The named field is a tool's, not a picture's, so
			// it is the request field the upstream refuses (#401).
			name:   "a tool schema refusal that names an image tool",
			status: 400,
			body: `{"error":{"code":"invalid_function_parameters","message":"Invalid schema for function 'refine_image': ` +
				`In context=('properties', 'width'), 'format' is not permitted.","param":"tools[3].function.parameters","type":"invalid_request_error"}}`,
			param: "tools[3].function.parameters",
		},
		{
			// The same refusal from a vendor that names no field: it is about
			// a tool's schema, so it is no picture class either.
			name:   "a tool schema refusal without a named field",
			status: 400,
			body: `{"error":{"code":"invalid_parameter_error","message":"Invalid schema for function 'create_model_3d_from_image': ` +
				`property 'resolution' has too large a maximum","type":"invalid_parameter_error"}}`,
		},
		{
			// A picture refusal that names the picture's own field stays a
			// bad picture: the field is in the messages.
			name:    "a picture refusal that names the picture's field",
			status:  400,
			body:    `{"error":{"message":"Invalid image: the image format is not supported","param":"messages[1].content[1].image_url","type":"invalid_request_error"}}`,
			invalid: []string{"format"},
		},
		{
			name:   "a context too long",
			status: 400,
			body:   `{"error":{"code":"context_length_exceeded","message":"This model's maximum context length is 8192 tokens"}}`,
		},
		{
			// The wording of a content refusal without a code the moderation
			// table knows: neither picture class owns it, so it stays a plain
			// failure. It is asserted so the day someone widens the capability
			// phrases this goes red instead of silently turning a moderation
			// refusal into "switch models".
			name:   "a content wording without a known moderation code",
			status: 400,
			body:   `{"error":{"message":"image data may contain inappropriate content"}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			refusal := upstreamhttp.Refusal{Status: test.status, Body: []byte(test.body), HasImages: true}
			err := refusal.Err()
			if got := errors.Is(err, agents.ErrImagesRejected); got != test.rejected {
				t.Fatalf("images rejected = %t, want %t: %v", got, test.rejected, err)
			}
			if got := strings.Join(agents.ImageInvalidReasons(err), ","); got != strings.Join(test.invalid, ",") {
				t.Fatalf("reasons = %q, want %q: %v", got, strings.Join(test.invalid, ","), err)
			}
			if len(test.invalid) > 0 && errors.Is(err, agents.ErrImagesRejected) {
				t.Fatalf("a bad picture is also a model without image input: %v", err)
			}
			if len(test.invalid) == 0 && errors.Is(err, agents.ErrImageInvalid) {
				t.Fatalf("misread refusal: %v", err)
			}
			if param, rejected := agents.RequestRejectedParam(err); rejected != (test.param != "") || param != test.param {
				t.Fatalf("param = %q rejected = %t, want %q: %v", param, rejected, test.param, err)
			}
			// The status travels as a StatusError and the upstream's wording
			// stays in the error, which only the log reads.
			var status *agents.StatusError
			if !errors.As(err, &status) || status.Status != test.status {
				t.Fatalf("status error = %v", err)
			}
			if !strings.Contains(err.Error(), strings.TrimSpace(strings.TrimPrefix(test.body, "data: "))) {
				t.Fatalf("the refusal body is not in the error: %v", err)
			}
		})
	}
}

// Only a request that carried pictures is read as a picture refusal at all.
func TestRefusalIgnoresThePictureClassesWithoutPictures(t *testing.T) {
	refusal := upstreamhttp.Refusal{Status: 400, Body: []byte(devTestPictureRefusal)}
	err := refusal.Err()
	if errors.Is(err, agents.ErrImageInvalid) || errors.Is(err, agents.ErrImagesRejected) {
		t.Fatalf("text only: %v", err)
	}
}
