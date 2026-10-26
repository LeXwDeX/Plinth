package app

import (
	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// inputImageMessages maps the Schema's refusal texts for input images that
// cannot be sent within an Offering's limits (#276).
func inputImageMessages(schema catalog.InputAssetRuntimeSchema) inputassets.Messages {
	messages := schema.Messages
	return inputassets.Messages{
		ParameterTooLarge:       messages.ParameterTooLargeTemplate,
		CompressionInsufficient: messages.CompressionInsufficientTemplate,
		TransparencyKept:        messages.TransparencyKeptTemplate,
		TotalTooLarge:           messages.TotalTooLargeTemplate,
		TooManyPixels:           messages.TooManyPixelsTemplate,
		TooSmall:                messages.TooSmallTemplate,
		AspectRatio:             messages.AspectRatioTemplate,
		UnreadableImage:         messages.UnreadableImageTemplate,
	}
}

// inputImageFitting maps the Schema's image_fitting policy (#276).
func inputImageFitting(schema catalog.InputAssetRuntimeSchema) inputassets.Fitting {
	fitting := schema.ImageFitting
	return inputassets.Fitting{
		ParameterSemantics: append([]string(nil), fitting.ParameterSemantics...),
		Policy: imagefit.Policy{
			Steps:           append([]string(nil), fitting.Steps...),
			JPEGQualities:   append([]int(nil), fitting.JPEGQualities...),
			MaxDecodePixels: fitting.MaxDecodePixels,
		},
	}
}
