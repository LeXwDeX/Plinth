package canvas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imaging"
	_ "golang.org/x/image/webp"
)

var (
	ErrInvalidMerge   = errors.New("canvas: invalid image merge")
	ErrDimensionLimit = errors.New("canvas: image merge dimension limit")
)

type MergeReferenceResolver interface {
	Resolve(
		context.Context,
		generation.OwnerID,
		[]string,
	) ([]generation.AssetID, error)
}

type MergeAssetGateway interface {
	OpenOwned(
		context.Context,
		generation.OwnerID,
		generation.AssetID,
	) (generation.InputAsset, io.ReadCloser, error)
	UploadAsset(
		context.Context,
		generation.OwnerID,
		generation.InputAssetUpload,
	) (generation.InputAsset, error)
}

type MergeNode struct {
	ImageURL string
	X        float64
	Y        float64
	Width    float64
	Height   float64
}

type ImageServiceConfig struct {
	References        MergeReferenceResolver
	Assets            MergeAssetGateway
	Clock             func() time.Time
	DecodeImage       func(io.Reader) (image.Image, string, error)
	MaximumNodes      int
	MaximumDimension  int
	MaximumAssetBytes int64
	FilenamePrefix    string
	Extension         string
	MediaType         string
	AssetType         string
}

type ImageService struct {
	references        MergeReferenceResolver
	assets            MergeAssetGateway
	clock             func() time.Time
	decodeImage       func(io.Reader) (image.Image, string, error)
	maximumNodes      int
	maximumDimension  int
	maximumAssetBytes int64
	filenamePrefix    string
	extension         string
	mediaType         string
	assetType         string
}

func NewImageService(config ImageServiceConfig) (*ImageService, error) {
	if config.References == nil || config.Assets == nil {
		return nil, errors.New("canvas: incomplete image dependencies")
	}
	if config.MaximumNodes <= 0 ||
		config.MaximumDimension <= 0 ||
		config.MaximumAssetBytes <= 0 {
		return nil, errors.New("canvas: invalid image limits")
	}
	for name, value := range map[string]string{
		"filename prefix": config.FilenamePrefix,
		"extension":       config.Extension,
		"media type":      config.MediaType,
		"asset type":      config.AssetType,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("canvas: image %s is required", name)
		}
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.DecodeImage == nil {
		config.DecodeImage = image.Decode
	}
	return &ImageService{
		references:        config.References,
		assets:            config.Assets,
		clock:             config.Clock,
		decodeImage:       config.DecodeImage,
		maximumNodes:      config.MaximumNodes,
		maximumDimension:  config.MaximumDimension,
		maximumAssetBytes: config.MaximumAssetBytes,
		filenamePrefix:    config.FilenamePrefix,
		extension:         config.Extension,
		mediaType:         config.MediaType,
		assetType:         config.AssetType,
	}, nil
}

func (service *ImageService) Merge(
	ctx context.Context,
	owner generation.OwnerID,
	nodes []MergeNode,
) (generation.InputAsset, error) {
	if owner == "" || len(nodes) == 0 || len(nodes) > service.maximumNodes {
		return generation.InputAsset{}, ErrInvalidMerge
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	urls := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if !validMergeNode(node) {
			return generation.InputAsset{}, ErrInvalidMerge
		}
		minX = math.Min(minX, node.X)
		minY = math.Min(minY, node.Y)
		maxX = math.Max(maxX, node.X+node.Width)
		maxY = math.Max(maxY, node.Y+node.Height)
		urls = append(urls, node.ImageURL)
	}
	widthValue := math.Ceil(maxX - minX)
	heightValue := math.Ceil(maxY - minY)
	if !finite(widthValue) ||
		!finite(heightValue) ||
		widthValue <= 0 ||
		heightValue <= 0 {
		return generation.InputAsset{}, ErrInvalidMerge
	}
	if widthValue > float64(service.maximumDimension) ||
		heightValue > float64(service.maximumDimension) {
		return generation.InputAsset{}, ErrDimensionLimit
	}
	canvasWidth, canvasHeight := int(widthValue), int(heightValue)
	target := image.NewNRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))

	assetIDs, err := service.references.Resolve(ctx, owner, urls)
	if err != nil {
		return generation.InputAsset{}, fmt.Errorf(
			"canvas: resolve merge references: %w",
			err,
		)
	}
	for index, assetID := range assetIDs {
		source, openErr := service.openImage(ctx, owner, assetID)
		if openErr != nil {
			return generation.InputAsset{}, openErr
		}
		node := nodes[index]
		nodeWidth := int(math.Round(node.Width))
		nodeHeight := int(math.Round(node.Height))
		if nodeWidth <= 0 || nodeHeight <= 0 {
			return generation.InputAsset{}, ErrInvalidMerge
		}
		resized := imaging.ResizeBilinear(
			source,
			nodeWidth,
			nodeHeight,
		)
		left := int(math.Round(node.X - minX))
		top := int(math.Round(node.Y - minY))
		draw.Draw(
			target,
			image.Rect(left, top, left+nodeWidth, top+nodeHeight),
			resized,
			image.Point{},
			draw.Over,
		)
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, target); err != nil {
		return generation.InputAsset{}, fmt.Errorf(
			"canvas: encode merged PNG: %w",
			err,
		)
	}
	filename := fmt.Sprintf(
		"%s-%d%s",
		service.filenamePrefix,
		service.clock().UTC().UnixMilli(),
		service.extension,
	)
	asset, err := service.assets.UploadAsset(
		ctx,
		owner,
		generation.InputAssetUpload{
			Body:             bytes.NewReader(encoded.Bytes()),
			MediaType:        service.mediaType,
			OriginalFilename: filename,
			AssetType:        service.assetType,
			MaxBytes:         service.maximumAssetBytes,
		},
	)
	if err != nil {
		return generation.InputAsset{}, fmt.Errorf(
			"canvas: persist merged image: %w",
			err,
		)
	}
	return asset, nil
}

func (service *ImageService) openImage(
	ctx context.Context,
	owner generation.OwnerID,
	assetID generation.AssetID,
) (image.Image, error) {
	_, body, err := service.assets.OpenOwned(ctx, owner, assetID)
	if err != nil {
		return nil, fmt.Errorf("canvas: open merge asset: %w", err)
	}
	defer body.Close()
	content, err := io.ReadAll(io.LimitReader(
		body,
		service.maximumAssetBytes+1,
	))
	if err != nil {
		return nil, fmt.Errorf("canvas: read merge asset: %w", err)
	}
	if int64(len(content)) > service.maximumAssetBytes {
		return nil, fmt.Errorf("canvas: merge asset exceeds byte limit")
	}
	configuration, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("canvas: inspect merge image: %w", err)
	}
	maximumPixels := int64(service.maximumDimension) *
		int64(service.maximumDimension)
	if configuration.Width <= 0 ||
		configuration.Height <= 0 ||
		int64(configuration.Width)*int64(configuration.Height) > maximumPixels {
		return nil, ErrDimensionLimit
	}
	decoded, _, err := service.decodeImage(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("canvas: decode merge image: %w", err)
	}
	return decoded, nil
}

func validMergeNode(node MergeNode) bool {
	return strings.TrimSpace(node.ImageURL) != "" &&
		finite(node.X) &&
		finite(node.Y) &&
		finite(node.Width) &&
		finite(node.Height) &&
		node.Width > 0 &&
		node.Height > 0
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
