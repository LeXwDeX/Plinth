package legacygeneration

import (
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type ProjectorConfig struct {
	Capabilities  []catalog.CapabilityPresentation
	Aliases       []catalog.TaskStreamCapabilityAlias
	ExcludedRoles []string
}

type Projector struct {
	capabilities map[generation.Capability]catalog.CapabilityPresentation
	aliases      map[string]generation.Capability
	excluded     map[string]struct{}
}

func NewProjector(config ProjectorConfig) (*Projector, error) {
	if len(config.Capabilities) == 0 {
		return nil, errors.New(
			"legacy generation projector: capabilities are missing",
		)
	}
	projector := &Projector{
		capabilities: make(
			map[generation.Capability]catalog.CapabilityPresentation,
			len(config.Capabilities),
		),
		aliases:  make(map[string]generation.Capability, len(config.Aliases)),
		excluded: make(map[string]struct{}, len(config.ExcludedRoles)),
	}
	for _, capability := range config.Capabilities {
		if capability.ID == "" || capability.LegacyAPI == nil {
			continue
		}
		if _, exists := projector.capabilities[capability.ID]; exists {
			return nil, errors.New(
				"legacy generation projector: duplicate capability",
			)
		}
		projector.capabilities[capability.ID] = capability
	}
	if len(projector.capabilities) == 0 {
		return nil, errors.New(
			"legacy generation projector: no legacy capabilities",
		)
	}
	for _, alias := range config.Aliases {
		externalType := strings.TrimSpace(alias.ExternalType)
		if externalType == "" || alias.Capability == "" {
			return nil, errors.New(
				"legacy generation projector: invalid alias",
			)
		}
		projector.aliases[externalType] = alias.Capability
	}
	for _, role := range config.ExcludedRoles {
		role = strings.TrimSpace(role)
		if role == "" {
			return nil, errors.New(
				"legacy generation projector: empty excluded role",
			)
		}
		projector.excluded[role] = struct{}{}
	}
	return projector, nil
}

func (projector *Projector) ProjectLegacyGenerationAssets(
	taskType string,
	providerData json.RawMessage,
	fallbackURLs []string,
) []administration.GenerationAsset {
	capabilityID := generation.Capability(strings.TrimSpace(taskType))
	if alias, exists := projector.aliases[string(capabilityID)]; exists {
		capabilityID = alias
	}
	capability, exists := projector.capabilities[capabilityID]
	if !exists || capability.LegacyAPI == nil {
		return []administration.GenerationAsset{}
	}
	data := map[string]any{}
	if !json.Valid(providerData) ||
		json.Unmarshal(providerData, &data) != nil {
		data = map[string]any{}
	}
	result := make([]administration.GenerationAsset, 0)
	seen := map[string]struct{}{}
	add := func(
		role string,
		variant string,
		source string,
		mediaType string,
	) {
		role = strings.TrimSpace(role)
		source = strings.TrimSpace(source)
		if !downloadSource(source) {
			return
		}
		if _, excluded := projector.excluded[role]; excluded {
			return
		}
		if _, duplicate := seen[source]; duplicate {
			return
		}
		seen[source] = struct{}{}
		result = append(result, administration.GenerationAsset{
			ID:                "legacy-" + strconv.Itoa(len(result)+1),
			Role:              role,
			Variant:           strings.TrimSpace(variant),
			MediaType:         strings.TrimSpace(mediaType),
			Ordinal:           len(result),
			SourceURL:         source,
			PreferredFilename: sourceFilename(source),
		})
	}

	for _, projection := range capability.LegacyAPI.OutputProjections {
		projectProjection(
			data[projection.Name],
			projection,
			capability.LegacyAPI.AssetDownload,
			add,
		)
	}
	projectResultFields(data, *capability.LegacyAPI, add)

	if len(result) == 0 {
		role := strings.TrimSpace(capability.LegacyAPI.OutputKind)
		for index, source := range fallbackURLs {
			add(role, strconv.Itoa(index), source, "")
		}
	}
	return result
}

type addProjectedAsset func(
	role string,
	variant string,
	source string,
	mediaType string,
)

func projectProjection(
	value any,
	projection catalog.LegacyOutputProjectionSchema,
	assetDownload *catalog.LegacyAssetDownloadSchema,
	add addProjectedAsset,
) {
	switch projection.Mode {
	case "scalar":
		if source, ok := value.(string); ok {
			add(projection.Role, projection.DefaultKey, source, "")
		}
	case "array":
		values, _ := value.([]any)
		for index, item := range values {
			if source, ok := item.(string); ok {
				add(
					projection.Role,
					strconv.Itoa(index),
					source,
					"",
				)
			}
		}
	case "map":
		projectMap(value, projection.Role, "", add)
	case "array-map":
		values, _ := value.([]any)
		for index, item := range values {
			projectMap(
				item,
				projection.Role,
				strconv.Itoa(index)+".",
				add,
			)
		}
	case "records":
		projectRecords(value, projection, assetDownload, add)
	}
}

func projectMap(
	value any,
	role string,
	prefix string,
	add addProjectedAsset,
) {
	values, ok := value.(map[string]any)
	if !ok {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if source, ok := values[key].(string); ok {
			add(role, prefix+key, source, "")
		}
	}
}

func projectRecords(
	value any,
	projection catalog.LegacyOutputProjectionSchema,
	assetDownload *catalog.LegacyAssetDownloadSchema,
	add addProjectedAsset,
) {
	var (
		sourceField       catalog.LegacyRecordFieldSchema
		identifierField   string
		downloadableField string
	)
	for _, field := range projection.RecordFields {
		if field.Semantic == "download-path" {
			sourceField = field
		}
		switch field.Source {
		case "provider-record-identifier":
			identifierField = field.Name
		case "provider-record-downloadable":
			downloadableField = field.Name
		}
	}
	if sourceField.Name == "" {
		return
	}
	records, _ := value.([]any)
	successfulOrdinal := 0
	for _, item := range records {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		source, _ := record[sourceField.Name].(string)
		if sourceField.Source ==
			"legacy-download-url-by-successful-ordinal" {
			downloadable, _ := record[downloadableField].(bool)
			token, _ := record[identifierField].(string)
			if !downloadable || assetDownload == nil ||
				strings.TrimSpace(token) == "" {
				continue
			}
			placeholder := "{" + assetDownload.TokenParameter + "}"
			source = strings.ReplaceAll(
				assetDownload.Path,
				placeholder,
				token,
			)
		}
		add(
			projection.Role,
			strconv.Itoa(successfulOrdinal),
			source,
			"",
		)
		successfulOrdinal++
	}
}

func projectResultFields(
	data map[string]any,
	schema catalog.LegacyTaskAPISchema,
	add addProjectedAsset,
) {
	if strings.TrimSpace(schema.ResultField) == "" {
		return
	}
	result, ok := data[schema.ResultField].(map[string]any)
	if !ok {
		return
	}
	for _, asset := range schema.ResultAssets {
		source, _ := result[asset.Name].(string)
		mediaType := ""
		if len(asset.MediaTypes) > 0 {
			mediaType = asset.MediaTypes[0]
		}
		add(asset.Role, asset.Variant, source, mediaType)
	}
	for _, collection := range schema.ResultCollections {
		values, ok := result[collection.Name].(map[string]any)
		if !ok {
			continue
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			source, _ := values[key].(string)
			add(
				collection.Role,
				collection.VariantPrefix+key,
				source,
				"",
			)
		}
	}
}

func downloadSource(value string) bool {
	return strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "https://") ||
		strings.HasPrefix(value, "http://")
}

func sourceFilename(source string) string {
	parsed, err := url.Parse(source)
	if err != nil {
		return ""
	}
	filename := path.Base(parsed.Path)
	if decoded, decodeErr := url.PathUnescape(filename); decodeErr == nil {
		filename = decoded
	}
	if filename == "." || filename == "/" {
		return ""
	}
	return filename
}
