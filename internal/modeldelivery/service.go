// Package modeldelivery projects what a finished 3D task delivers as
// downloadable files, plans the processing steps that fill missing formats,
// and packs the already-isolated local files into one ZIP.
//
// It owns no task state: every conversion is a Processing Plan whose steps the
// single task kernel runs, and every file it serves is a READY local Asset of
// an owner-visible Task. Which formats exist, which Offerings may fill them and
// what the ZIP says are all declared in the Product Schema (model_delivery).
package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

var (
	// ErrNotApplicable: the task is not a finished 3D model task.
	ErrNotApplicable = errors.New("任务不是可交付多种格式的 3D 模型任务")
	// ErrNothingToComplete: no missing format can be obtained any more.
	ErrNothingToComplete = errors.New("没有可以补齐的格式")
	// ErrEmptyArchive: the task has no saved model file to pack.
	ErrEmptyArchive = errors.New("任务没有可打包的模型文件")
	// ErrArchiveTooLarge: the files exceed the Schema's size or entry bound.
	ErrArchiveTooLarge = errors.New("模型文件超过一键打包的上限")
)

// Gateway is the read side of the task kernel and the Offering catalog.
type Gateway interface {
	GetScoped(context.Context, generation.TaskListScope, generation.TaskID) (generation.Task, error)
	OpenAssetScoped(context.Context, generation.TaskListScope, generation.TaskID, generation.AssetID) (generation.AssetContent, error)
	Offering(generation.OfferingID) (generation.Offering, bool)
	OfferingAvailable(generation.OfferingID) bool
	ValidateOfferingInput(context.Context, generation.OwnerID, generation.OfferingID, string, json.RawMessage) error
}

// RefetchReader is the optional kernel read of a failed Task's upstream
// result refetch (#295). With it the format list says what a failed paid
// conversion already charged, offers the free refetch first and takes the
// files of a refetch that delivered (#330).
type RefetchReader interface {
	RefetchEligibility(context.Context, generation.OwnerID, generation.TaskID) (generation.RefetchEligibility, error)
}

// SessionTaskLister is the optional kernel listing of a session's Tasks. The
// Agent can run a declared conversion directly, without a Processing Plan
// (#330); the formats projection reads those Tasks by their frozen source so
// their delivered files count and a running one is not started twice.
type SessionTaskLister interface {
	List(context.Context, generation.TaskListScope, generation.ListQuery) (generation.TaskPage, error)
}

// Plans is the Processing Plan service. Conversions run as plans so that
// ordering, approval policy, idempotency and recovery stay in one place.
type Plans interface {
	Create(context.Context, processing.CreateCommand) (processing.Plan, error)
	Get(context.Context, generation.OwnerID, string) (processing.Plan, error)
	// ListBySource returns every plan of the owner's session made from one
	// source task, uncapped. Completion plans are found by what they do, not
	// by their idempotency key, because the key carries the Offering revision
	// and the revision can change while a plan is still running; a capped
	// session listing could hide an old active plan and allow a second charge.
	ListBySource(context.Context, generation.OwnerID, string, generation.TaskID) ([]processing.Plan, error)
}

type Service struct {
	schema  catalog.ModelDeliverySchema
	gateway Gateway
	plans   Plans
	logger  *slog.Logger
	texts   *catalog.TextLocalizer
}

// Localize makes the archive README and the reasons follow the UI locale of
// the request in ctx.
func (s *Service) Localize(texts *catalog.TextLocalizer) { s.texts = texts }

// inLocale is the service reading its Schema copy in the UI locale of ctx.
func (s *Service) inLocale(ctx context.Context) *Service {
	locale := catalog.LocaleFrom(ctx)
	if !s.texts.Translates(locale) {
		return s
	}
	encoded, err := json.Marshal(s.schema)
	if err != nil {
		return s
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return s
	}
	if encoded, err = json.Marshal(s.texts.LocalizeJSON(locale, generic)); err != nil {
		return s
	}
	localized := *s
	if err := json.Unmarshal(encoded, &localized.schema); err != nil {
		return s
	}
	return &localized
}

func New(schema catalog.ModelDeliverySchema, gateway Gateway, plans Plans, logger *slog.Logger) (*Service, error) {
	if schema.Disabled() {
		return nil, errors.New("Product Schema 没有声明 model_delivery")
	}
	if gateway == nil || plans == nil {
		return nil, errors.New("模型交付缺少任务内核或处理计划服务")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{schema: schema, gateway: gateway, plans: plans, logger: logger}, nil
}

const (
	modelRole     = "model"
	convertedRole = "converted_model"
)

// ready reports whether the Asset is a saved local file.
func ready(asset generation.Asset) bool { return asset.StorageKey != "" }

func sortedAssets(task generation.Task) []generation.Asset {
	assets := make([]generation.Asset, 0, len(task.Assets))
	for _, asset := range task.Assets {
		if ready(asset) {
			assets = append(assets, asset)
		}
	}
	sort.SliceStable(assets, func(i, j int) bool {
		if assets[i].Ordinal != assets[j].Ordinal {
			return assets[i].Ordinal < assets[j].Ordinal
		}
		return assets[i].ID < assets[j].ID
	})
	return assets
}

func matchesAsset(match catalog.ModelAssetMatch, asset generation.Asset) bool {
	return match.Matches(asset.Role, asset.Variant, asset.MediaType)
}

// DownloadFilename names a task's saved file for a download on its own by
// the format it is: the first format whose matches select it, as the archive
// classifies files, with the archive's extension rule. Files of no format
// (images, reports, material libraries) get no name.
func (s *Service) DownloadFilename(taskID generation.TaskID, asset generation.Asset) (string, bool) {
	template := s.schema.DownloadFilenameTemplate
	if template == "" {
		return "", false
	}
	for _, format := range s.schema.Formats {
		if !s.formatMatches(format, asset) {
			continue
		}
		name := strings.NewReplacer("{task_id}", string(taskID), "{extension}", s.formatExtension(format, asset.MediaType)).Replace(template)
		// Header-safe: an ID is never trusted to stay within a token.
		return strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
				return r
			}
			return '_'
		}, name), true
	}
	return "", false
}

// DownloadMediaType is the Content-Type of a single download of a saved
// model file (#330): its format's Schema download_media_type, so an OBJ the
// host's mime table called application/x-tgif or a USDZ saved as a ZIP is
// served as the model type it is. A packaged variant (a saved media type the
// format lists in media_type_extensions, e.g. a glTF or OBJ ZIP) keeps its
// own type. A file of no format is not named.
func (s *Service) DownloadMediaType(asset generation.Asset) (string, bool) {
	for _, format := range s.schema.Formats {
		if !s.formatMatches(format, asset) {
			continue
		}
		saved := strings.ToLower(strings.TrimSpace(strings.Split(asset.MediaType, ";")[0]))
		if _, packaged := format.MediaTypeExtensions[saved]; packaged {
			return saved, true
		}
		if format.DownloadMediaType == "" {
			return "", false
		}
		return format.DownloadMediaType, true
	}
	return "", false
}

// formatMediaType is the public media type of a format row: the same one the
// single-file download route serves. A saved file that matches no format keeps
// its stored label; every row of a target format is normalized.
func (s *Service) formatMediaType(asset generation.Asset) string {
	if mediaType, ok := s.DownloadMediaType(asset); ok {
		return mediaType
	}
	return asset.MediaType
}

func (s *Service) formatMatches(format catalog.ModelFormatSchema, asset generation.Asset) bool {
	for _, match := range format.Matches {
		if matchesAsset(match, asset) {
			return true
		}
	}
	return false
}

// classify assigns READY assets to target formats: of each format the first
// matching asset by the task's main-model order (#326: a required output
// before an attachment, then by ordinal; generation.PrimaryModelAsset), so a
// format row shows the same file the stage and the 加工 panel use. The second
// return value holds the assets that match no format, in their given order.
func (s *Service) classify(assets []generation.Asset) (map[string]generation.Asset, []generation.Asset) {
	byFormat := make(map[string]generation.Asset, len(s.schema.Formats))
	var rest []generation.Asset
	ordered := slices.Clone(assets)
	slices.SortStableFunc(ordered, func(left, right generation.Asset) int {
		switch {
		case left.Required == right.Required:
			return 0
		case left.Required:
			return -1
		}
		return 1
	})
	unassigned := map[generation.AssetID]bool{}
	for _, asset := range ordered {
		assigned := false
		for _, format := range s.schema.Formats {
			if _, taken := byFormat[format.ID]; taken || !s.formatMatches(format, asset) {
				continue
			}
			byFormat[format.ID] = asset
			assigned = true
			break
		}
		if !assigned {
			unassigned[asset.ID] = true
		}
	}
	for _, asset := range assets {
		if unassigned[asset.ID] {
			rest = append(rest, asset)
		}
	}
	return byFormat, rest
}

func (s *Service) format(id string) (catalog.ModelFormatSchema, bool) {
	for _, format := range s.schema.Formats {
		if format.ID == id {
			return format, true
		}
	}
	return catalog.ModelFormatSchema{}, false
}

func (s *Service) capabilityIn(capability generation.Capability, list []generation.Capability) bool {
	for _, candidate := range list {
		if candidate == capability {
			return true
		}
	}
	return false
}

func (s *Service) reasonText(code string) string {
	reasons := s.schema.Reasons
	switch code {
	case reasonNoRoute:
		return reasons.NoRoute
	case reasonSourceBlocked:
		return reasons.SourceBlocked
	case reasonSourceIncompatible:
		return reasons.SourceIncompatible
	case reasonOfferingUnavailable:
		return reasons.OfferingUnavailable
	case reasonAssetUnreadable:
		return reasons.AssetUnreadable
	case reasonNotConverted:
		return reasons.NotConverted
	case reasonConverting:
		return reasons.Converting
	case reasonConversionIncomplete:
		return reasons.ConversionIncomplete
	case reasonConversionFailed:
		return reasons.ConversionFailed
	case reasonSourceExpired:
		return reasons.SourceExpired
	}
	return ""
}

// Reason codes. The user-facing text of each is declared in the Product
// Schema (model_delivery.reasons).
const (
	reasonNoRoute              = "no_route"
	reasonSourceBlocked        = "source_blocked"
	reasonSourceIncompatible   = "source_incompatible"
	reasonOfferingUnavailable  = "offering_unavailable"
	reasonAssetUnreadable      = "asset_unreadable"
	reasonNotConverted         = "not_converted"
	reasonConverting           = "converting"
	reasonConversionIncomplete = "conversion_incomplete"
	reasonConversionFailed     = "conversion_failed"
	reasonSourceExpired        = "source_expired"
)

func wrap(operation string, err error) error {
	return fmt.Errorf("modeldelivery %s: %w", operation, err)
}
