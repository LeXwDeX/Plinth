package catalog

import (
	"encoding/json"
	"fmt"
	"mime"
	"slices"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// ModelDeliverySchema declares what a finished 3D task is expected to deliver
// to the user as downloadable files, how a missing file format can be obtained
// through an existing processing Offering, and what the one-click ZIP contains.
// It is server-side only: the workspace reads the resulting coverage
// projection, never this block.
type ModelDeliverySchema struct {
	// Capabilities are the task capabilities whose results the coverage
	// projection and the "complete formats" action apply to.
	Capabilities []schemacontract.Capability `json:"capabilities"`
	// Formats lists the target formats in presentation order.
	Formats []ModelFormatSchema `json:"formats"`
	// Conversions lists, in priority order, the Offerings that can produce a
	// missing format. An Offering is only used when its own input contract
	// accepts the task (or one of its saved model files) as the source.
	Conversions []ModelConversionSchema `json:"conversions"`
	// BlockedCapabilities are task kinds whose results must not be sent
	// through a format conversion (it would drop what makes them special,
	// e.g. a skeleton), so the missing formats are reported as unavailable.
	BlockedCapabilities []schemacontract.Capability `json:"blocked_capabilities"`
	// Reasons maps a reason code to the user-facing text shown when a format
	// is unavailable and written to the ZIP's README.
	Reasons ModelDeliveryReasons `json:"reasons"`
	Archive ModelArchiveSchema   `json:"archive"`
	// DownloadFilenameTemplate names a saved model file downloaded on its own
	// through the task asset route ({task_id}, {extension} as in the archive).
	// Empty leaves single downloads without a suggested file name.
	DownloadFilenameTemplate string `json:"download_filename_template,omitempty"`
}

// Disabled reports a Product Schema without model delivery declarations.
func (delivery ModelDeliverySchema) Disabled() bool {
	return len(delivery.Capabilities) == 0 && len(delivery.Formats) == 0 &&
		len(delivery.Conversions) == 0 && delivery.Archive.FilenameTemplate == ""
}

// ModelAssetMatch selects saved task assets by role, variant and media type.
// An empty Variants entry matches an asset without a variant.
type ModelAssetMatch struct {
	Roles           []string `json:"roles"`
	Variants        []string `json:"variants,omitempty"`
	VariantPrefixes []string `json:"variant_prefixes,omitempty"`
	MediaTypes      []string `json:"media_types,omitempty"`
}

type ModelFormatSchema struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Extension is the file extension (with dot) used when the saved media
	// type does not decide it.
	Extension string `json:"extension"`
	// Directory is the folder of this format inside the ZIP.
	Directory string `json:"directory"`
	// ExtensionFromMediaType lets the saved media type decide the extension
	// (a glTF delivered as a binary container is a .glb file).
	ExtensionFromMediaType bool `json:"extension_from_media_type,omitempty"`
	// MediaTypeExtensions decides this format's extension by saved media type
	// before the archive-wide table (a glTF package ZIP is a .gltf.zip file).
	MediaTypeExtensions map[string]string `json:"media_type_extensions,omitempty"`
	// DownloadMediaType is the Content-Type of a single download of this
	// format's file (#330): the IANA registration where one exists, whatever
	// label the file was saved with. A saved media type listed in
	// MediaTypeExtensions (a packaged variant, e.g. a ZIP) is kept instead.
	DownloadMediaType string            `json:"download_media_type"`
	Matches           []ModelAssetMatch `json:"matches"`
}

// ModelConversionSchema names one Offering that can fill missing formats.
// Provides lists the formats one run delivers together. Alternatively
// FormatParameter and FormatValues describe an Offering that converts to one
// format per run. Input holds the fixed parameters of the run.
type ModelConversionSchema struct {
	OfferingID      string            `json:"offering_id"`
	Input           json.RawMessage   `json:"input,omitempty"`
	Provides        []string          `json:"provides,omitempty"`
	FormatParameter string            `json:"format_parameter,omitempty"`
	FormatValues    map[string]string `json:"format_values,omitempty"`
	// SourceFormats orders the saved formats tried first as the source file.
	SourceFormats []string `json:"source_formats"`
	// Retired keeps a route that is no longer used (#330: Remesh with
	// convert_format_only, replaced by Meshy Convert) only so completion
	// plans made under it are still recognized by their fixed input: their
	// delivered files and running state count, but no new run is planned and
	// the Agent never routes to it. Its input may name a parameter the
	// Offering no longer declares; it is never sent.
	Retired bool `json:"retired,omitempty"`
}

// ModelDeliveryReasons are the reason texts, keyed by code.
type ModelDeliveryReasons struct {
	NoRoute             string `json:"no_route"`
	SourceBlocked       string `json:"source_blocked"`
	SourceIncompatible  string `json:"source_incompatible"`
	OfferingUnavailable string `json:"offering_unavailable"`
	AssetUnreadable     string `json:"asset_unreadable"`
	// NotConverted is shown for a format that can be obtained but has not
	// been; Converting while its conversion is still running.
	NotConverted string `json:"not_converted"`
	Converting   string `json:"converting"`
	// ConversionIncomplete is shown when a conversion finished without
	// delivering the format it was started for.
	ConversionIncomplete string `json:"conversion_incomplete"`
	// ConversionFailed is shown for a format whose last conversion failed
	// (#330): it can be fetched, retried or converted again from the
	// platform's formats tab, unlike a format that was never converted.
	ConversionFailed string `json:"conversion_failed"`
	// SourceExpired is shown when the only route that could convert the
	// format names the source's original provider task and that task has
	// passed its retention (#325), while no route takes the saved file.
	SourceExpired string `json:"source_expired"`
}

type ModelArchiveSchema struct {
	// FilenameTemplate uses {task_id}.
	FilenameTemplate string `json:"filename_template"`
	MediaType        string `json:"media_type"`
	// FileBasename is the base file name of each format's model file.
	FileBasename string `json:"file_basename"`
	// OtherDirectory holds saved assets that match no target format and are
	// not part of an OBJ package (e.g. animation clips).
	OtherDirectory string `json:"other_directory"`
	// MediaTypeExtensions decides the extension of a saved asset by its
	// media type; Format.Extension is the fallback.
	MediaTypeExtensions map[string]string `json:"media_type_extensions"`
	// StoredMediaTypes are already compressed and are stored, not deflated.
	StoredMediaTypes []string                 `json:"stored_media_types"`
	MaxEntries       int                      `json:"max_entries"`
	MaxBytes         int64                    `json:"max_bytes"`
	Readme           ModelArchiveReadmeSchema `json:"readme"`
	ObjPackage       ModelObjPackageSchema    `json:"obj_package"`
	Audit            ModelArchiveAuditSchema  `json:"audit"`
}

// ModelObjPackageSchema declares how an OBJ file keeps working inside the
// ZIP: the material library is written under the name the OBJ references and
// textures under the names the material library references.
type ModelObjPackageSchema struct {
	FormatID string          `json:"format_id"`
	Material ModelAssetMatch `json:"material"`
	Textures ModelAssetMatch `json:"textures"`
	// ReferenceScanBytes bounds how much of the OBJ and material library is
	// read to find the references.
	ReferenceScanBytes int64 `json:"reference_scan_bytes"`
	// LibraryStatement is the OBJ line keyword that names the material
	// library file.
	LibraryStatement string `json:"library_statement"`
	// TextureBindings pair a saved texture (by the last dotted segment of
	// its variant) with the material statements that reference that kind of
	// texture.
	TextureBindings       []ModelTextureBinding `json:"texture_bindings"`
	MaterialFileExtension string                `json:"material_file_extension"`
	// ConvertedRoles and ConvertedProviders identify an OBJ that came out of a
	// provider's format conversion (asset role, and the provider of the Task
	// that produced it). Such an OBJ carries geometry only, so the README says
	// so when no material library or texture accompanies it. Both are empty
	// when the README has no such note.
	ConvertedRoles     []string `json:"converted_roles,omitempty"`
	ConvertedProviders []string `json:"converted_providers,omitempty"`
	// ConvertedOfferings are format-conversion Offerings whose OBJ result is
	// a conversion whatever its asset role (#330: Meshy Convert delivers its
	// files as ordinary models). The README note applies to them the same way.
	ConvertedOfferings []string `json:"converted_offerings,omitempty"`
}

type ModelTextureBinding struct {
	VariantKey string   `json:"variant_key"`
	Statements []string `json:"statements"`
}

// ModelArchiveReadmeSchema is the text of README.txt. {task_id}, {label},
// {path}, {reason} and {count} are substituted.
type ModelArchiveReadmeSchema struct {
	Filename              string `json:"filename"`
	Title                 string `json:"title"`
	TaskLine              string `json:"task_line"`
	IncludedHeading       string `json:"included_heading"`
	IncludedLine          string `json:"included_line"`
	MissingHeading        string `json:"missing_heading"`
	MissingLine           string `json:"missing_line"`
	NoneIncluded          string `json:"none_included"`
	DependencyHeading     string `json:"dependency_heading"`
	DependencyMissingLine string `json:"dependency_missing_line"`
	DependencyLine        string `json:"dependency_line"`
	OtherHeading          string `json:"other_heading"`
	OtherLine             string `json:"other_line"`
	CompleteNote          string `json:"complete_note"`
	IncompleteNote        string `json:"incomplete_note"`
	// ConvertedObjNote is written, with {path} replaced by the OBJ path, when
	// the OBJ is a conversion result without material library or textures.
	ConvertedObjNote string `json:"converted_obj_note,omitempty"`
	// ConvertedObjPackageNote replaces ConvertedObjNote for a converted OBJ
	// that was delivered as a package (a media type the OBJ format maps to
	// its own extension). {path} is the package path and {count} the number
	// of files it actually holds, read from its directory; each file then
	// follows on a dependency_line.
	ConvertedObjPackageNote string `json:"converted_obj_package_note,omitempty"`
	// ConvertedObjReferenceNote replaces ConvertedObjNote for a converted OBJ
	// that names a material library the conversion never delivers (#330: the
	// Meshy Convert OBJ is a Blender export with "mtllib temp.mtl" and UVs).
	// {path} is the OBJ path and {references} the names it references; they
	// are then not listed as files the task failed to save.
	ConvertedObjReferenceNote string `json:"converted_obj_reference_note,omitempty"`
}

type ModelArchiveAuditSchema struct {
	Action       string                        `json:"action"`
	ResourceType string                        `json:"resource_type"`
	DetailFields ModelArchiveAuditDetailFields `json:"detail_fields"`
}

type ModelArchiveAuditDetailFields struct {
	TaskID  string `json:"task_id"`
	Entries string `json:"entries"`
	Bytes   string `json:"bytes"`
	Missing string `json:"missing"`
}

func cloneModelDelivery(source ModelDeliverySchema) ModelDeliverySchema {
	result := source
	result.Capabilities = append([]schemacontract.Capability(nil), source.Capabilities...)
	result.BlockedCapabilities = append([]schemacontract.Capability(nil), source.BlockedCapabilities...)
	result.Formats = make([]ModelFormatSchema, len(source.Formats))
	for index, format := range source.Formats {
		result.Formats[index] = format
		result.Formats[index].Matches = cloneModelMatches(format.Matches)
		if format.MediaTypeExtensions != nil {
			result.Formats[index].MediaTypeExtensions = make(map[string]string, len(format.MediaTypeExtensions))
			for mediaType, extension := range format.MediaTypeExtensions {
				result.Formats[index].MediaTypeExtensions[mediaType] = extension
			}
		}
	}
	result.Conversions = make([]ModelConversionSchema, len(source.Conversions))
	for index, conversion := range source.Conversions {
		result.Conversions[index] = conversion
		result.Conversions[index].Input = append(json.RawMessage(nil), conversion.Input...)
		result.Conversions[index].Provides = append([]string(nil), conversion.Provides...)
		result.Conversions[index].SourceFormats = append([]string(nil), conversion.SourceFormats...)
		result.Conversions[index].FormatValues = make(map[string]string, len(conversion.FormatValues))
		for format, value := range conversion.FormatValues {
			result.Conversions[index].FormatValues[format] = value
		}
	}
	archive := &result.Archive
	archive.MediaTypeExtensions = make(map[string]string, len(source.Archive.MediaTypeExtensions))
	for mediaType, extension := range source.Archive.MediaTypeExtensions {
		archive.MediaTypeExtensions[mediaType] = extension
	}
	archive.StoredMediaTypes = append([]string(nil), source.Archive.StoredMediaTypes...)
	archive.ObjPackage.ConvertedRoles = append([]string(nil), source.Archive.ObjPackage.ConvertedRoles...)
	archive.ObjPackage.ConvertedProviders = append([]string(nil), source.Archive.ObjPackage.ConvertedProviders...)
	archive.ObjPackage.ConvertedOfferings = append([]string(nil), source.Archive.ObjPackage.ConvertedOfferings...)
	archive.ObjPackage.Material = cloneModelMatch(source.Archive.ObjPackage.Material)
	archive.ObjPackage.Textures = cloneModelMatch(source.Archive.ObjPackage.Textures)
	archive.ObjPackage.TextureBindings = make([]ModelTextureBinding, len(source.Archive.ObjPackage.TextureBindings))
	for index, binding := range source.Archive.ObjPackage.TextureBindings {
		archive.ObjPackage.TextureBindings[index] = ModelTextureBinding{
			VariantKey: binding.VariantKey,
			Statements: append([]string(nil), binding.Statements...),
		}
	}
	return result
}

func cloneModelMatches(source []ModelAssetMatch) []ModelAssetMatch {
	result := make([]ModelAssetMatch, len(source))
	for index, match := range source {
		result[index] = cloneModelMatch(match)
	}
	return result
}

func cloneModelMatch(source ModelAssetMatch) ModelAssetMatch {
	return ModelAssetMatch{
		Roles:           append([]string(nil), source.Roles...),
		Variants:        append([]string(nil), source.Variants...),
		VariantPrefixes: append([]string(nil), source.VariantPrefixes...),
		MediaTypes:      append([]string(nil), source.MediaTypes...),
	}
}

// validateModelDelivery checks internal consistency. References to
// Offerings are checked by the Catalog loader, which knows the Offerings.
func validateModelDelivery(schema ApplicationSchema) error {
	delivery := schema.ModelDelivery
	if delivery.Disabled() {
		// Historical and minimal Product Schemas do not declare the block;
		// the coverage, completion and ZIP routes are then not served.
		return nil
	}
	if len(delivery.Capabilities) == 0 || len(delivery.Formats) == 0 {
		return fmt.Errorf("capabilities 与 formats 不能为空")
	}
	known := map[schemacontract.Capability]struct{}{}
	for _, capability := range schema.Capabilities {
		known[capability.ID] = struct{}{}
	}
	for _, capability := range append(append([]schemacontract.Capability(nil), delivery.Capabilities...), delivery.BlockedCapabilities...) {
		if _, exists := known[capability]; !exists {
			return fmt.Errorf("引用了未知 capability %q", capability)
		}
	}
	formats := map[string]struct{}{}
	directories := map[string]struct{}{}
	for index, format := range delivery.Formats {
		if strings.TrimSpace(format.ID) == "" || strings.TrimSpace(format.Label) == "" ||
			!strings.HasPrefix(format.Extension, ".") || strings.TrimSpace(format.Directory) == "" ||
			len(format.Matches) == 0 {
			return fmt.Errorf("formats[%d] 不完整", index)
		}
		if _, exists := formats[format.ID]; exists {
			return fmt.Errorf("format %q 重复", format.ID)
		}
		if mediaType, _, err := mime.ParseMediaType(format.DownloadMediaType); err != nil || mediaType != format.DownloadMediaType || !strings.Contains(mediaType, "/") {
			return fmt.Errorf("format %q 的 download_media_type 无效", format.ID)
		}
		if _, exists := directories[format.Directory]; exists {
			return fmt.Errorf("format directory %q 重复", format.Directory)
		}
		formats[format.ID] = struct{}{}
		directories[format.Directory] = struct{}{}
		for _, match := range format.Matches {
			if len(match.Roles) == 0 {
				return fmt.Errorf("format %q 的 match 缺少 roles", format.ID)
			}
		}
		for mediaType, extension := range format.MediaTypeExtensions {
			if mediaType != strings.ToLower(mediaType) || !strings.Contains(mediaType, "/") || !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, "/\\") {
				return fmt.Errorf("format %q 的 media_type_extensions 无效", format.ID)
			}
		}
	}
	for index, conversion := range delivery.Conversions {
		if strings.TrimSpace(conversion.OfferingID) == "" || len(conversion.SourceFormats) == 0 {
			return fmt.Errorf("conversions[%d] 不完整", index)
		}
		if len(conversion.Provides) == 0 && len(conversion.FormatValues) == 0 {
			return fmt.Errorf("conversions[%d] 没有声明可提供的格式", index)
		}
		if len(conversion.FormatValues) > 0 && strings.TrimSpace(conversion.FormatParameter) == "" {
			return fmt.Errorf("conversions[%d] 声明 format_values 时必须声明 format_parameter", index)
		}
		for _, format := range append(append([]string(nil), conversion.Provides...), conversion.SourceFormats...) {
			if _, exists := formats[format]; !exists {
				return fmt.Errorf("conversions[%d] 引用了未知 format %q", index, format)
			}
		}
		for format := range conversion.FormatValues {
			if _, exists := formats[format]; !exists {
				return fmt.Errorf("conversions[%d] 引用了未知 format %q", index, format)
			}
		}
		if len(conversion.Input) > 0 {
			var input map[string]json.RawMessage
			if json.Unmarshal(conversion.Input, &input) != nil || input == nil {
				return fmt.Errorf("conversions[%d].input 必须是 JSON 对象", index)
			}
		}
		// A retired route is recognized by its fixed input and the formats
		// it provides; a per-format route has no single fingerprint.
		if conversion.Retired && (len(conversion.Provides) == 0 || len(conversion.FormatValues) > 0 || len(conversion.Input) == 0) {
			return fmt.Errorf("conversions[%d] 已退役的转换须以固定 input 与 provides 识别旧计划，不能按格式逐次转换", index)
		}
	}
	reasons := delivery.Reasons
	for name, text := range map[string]string{
		"no_route": reasons.NoRoute, "source_blocked": reasons.SourceBlocked,
		"source_incompatible":  reasons.SourceIncompatible,
		"offering_unavailable": reasons.OfferingUnavailable, "asset_unreadable": reasons.AssetUnreadable,
		"not_converted": reasons.NotConverted, "converting": reasons.Converting,
		"conversion_incomplete": reasons.ConversionIncomplete,
		"conversion_failed":     reasons.ConversionFailed, "source_expired": reasons.SourceExpired,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("reasons.%s 不能为空", name)
		}
	}
	if template := delivery.DownloadFilenameTemplate; template != "" &&
		(!strings.Contains(template, "{task_id}") || !strings.Contains(template, "{extension}") || strings.ContainsAny(template, "/\\\"';")) {
		return fmt.Errorf("download_filename_template 需要 {task_id} 与 {extension}，且不能含路径分隔符或引号")
	}
	archive := delivery.Archive
	if !strings.Contains(archive.FilenameTemplate, "{task_id}") || strings.TrimSpace(archive.MediaType) == "" ||
		strings.TrimSpace(archive.FileBasename) == "" || strings.TrimSpace(archive.OtherDirectory) == "" ||
		archive.MaxEntries <= 0 || archive.MaxBytes <= 0 {
		return fmt.Errorf("archive 不完整")
	}
	if _, exists := directories[archive.OtherDirectory]; exists {
		return fmt.Errorf("archive.other_directory 与 format directory 冲突")
	}
	object := archive.ObjPackage
	if _, exists := formats[object.FormatID]; !exists || len(object.Material.Roles) == 0 ||
		len(object.Textures.Roles) == 0 || object.ReferenceScanBytes <= 0 ||
		strings.TrimSpace(object.LibraryStatement) == "" || !strings.HasPrefix(object.MaterialFileExtension, ".") {
		return fmt.Errorf("archive.obj_package 不完整")
	}
	if len(object.ConvertedOfferings) > 0 && (archive.Readme.ConvertedObjNote == "" || archive.Readme.ConvertedObjReferenceNote == "") {
		return fmt.Errorf("archive.obj_package.converted_offerings 需要 readme.converted_obj_note 与 converted_obj_reference_note")
	}
	if note := archive.Readme.ConvertedObjReferenceNote; note != "" && (!strings.Contains(note, "{path}") || !strings.Contains(note, "{references}") || archive.Readme.ConvertedObjNote == "") {
		return fmt.Errorf("archive.readme.converted_obj_reference_note 需要 {path} 与 {references}，并同时声明 converted_obj_note")
	}
	if note := archive.Readme.ConvertedObjNote; note != "" || len(object.ConvertedRoles) > 0 || len(object.ConvertedProviders) > 0 {
		if !strings.Contains(note, "{path}") || len(object.ConvertedRoles) == 0 || len(object.ConvertedProviders) == 0 {
			return fmt.Errorf("archive.readme.converted_obj_note 需要 {path}，并同时声明 obj_package.converted_roles 与 converted_providers")
		}
	}
	if note := archive.Readme.ConvertedObjPackageNote; note != "" {
		if !strings.Contains(note, "{path}") || !strings.Contains(note, "{count}") || len(object.ConvertedRoles) == 0 || len(object.ConvertedProviders) == 0 {
			return fmt.Errorf("archive.readme.converted_obj_package_note 需要 {path} 与 {count}，并同时声明 obj_package.converted_roles 与 converted_providers")
		}
	}
	readme := archive.Readme
	if strings.TrimSpace(readme.Filename) == "" || strings.Contains(readme.Filename, "/") ||
		strings.TrimSpace(readme.Title) == "" || !strings.Contains(readme.TaskLine, "{task_id}") ||
		!strings.Contains(readme.IncludedLine, "{label}") || !strings.Contains(readme.IncludedLine, "{path}") ||
		!strings.Contains(readme.MissingLine, "{label}") || !strings.Contains(readme.MissingLine, "{reason}") ||
		!strings.Contains(readme.DependencyLine, "{path}") || !strings.Contains(readme.DependencyMissingLine, "{path}") || !strings.Contains(readme.OtherLine, "{path}") ||
		strings.TrimSpace(readme.IncludedHeading) == "" || strings.TrimSpace(readme.MissingHeading) == "" ||
		strings.TrimSpace(readme.NoneIncluded) == "" || strings.TrimSpace(readme.DependencyHeading) == "" ||
		strings.TrimSpace(readme.OtherHeading) == "" || strings.TrimSpace(readme.CompleteNote) == "" ||
		strings.TrimSpace(readme.IncompleteNote) == "" {
		return fmt.Errorf("archive.readme 不完整")
	}
	audit := archive.Audit
	if strings.TrimSpace(audit.Action) == "" || strings.TrimSpace(audit.ResourceType) == "" ||
		audit.DetailFields.TaskID == "" || audit.DetailFields.Entries == "" ||
		audit.DetailFields.Bytes == "" || audit.DetailFields.Missing == "" {
		return fmt.Errorf("archive.audit 不完整")
	}
	auditActions := map[string]struct{}{}
	for _, action := range schema.AuditActions {
		auditActions[action.Value] = struct{}{}
	}
	if _, exists := auditActions[audit.Action]; !exists {
		return fmt.Errorf("archive.audit.action 引用了未知 audit action %q", audit.Action)
	}
	return nil
}

// validateModelDeliveryOfferings checks every conversion against the loaded
// Offerings: it must be a processing Offering and, when it converts one
// format per run, must declare the format parameter with those values.
func validateModelDeliveryOfferings(
	delivery ModelDeliverySchema,
	offerings map[schemacontract.OfferingID]schemacontract.Offering,
) error {
	for index, conversion := range delivery.Conversions {
		offering, exists := offerings[schemacontract.OfferingID(conversion.OfferingID)]
		if !exists {
			return fmt.Errorf("conversions[%d] 引用了未知 Offering %q", index, conversion.OfferingID)
		}
		if offering.Domain != schemacontract.DomainProcessing {
			return fmt.Errorf("conversions[%d] 的 Offering %q 不是处理类 Offering", index, conversion.OfferingID)
		}
		var input map[string]json.RawMessage
		if len(conversion.Input) > 0 {
			_ = json.Unmarshal(conversion.Input, &input)
		}
		parameters := map[string]schemacontract.Parameter{}
		for _, parameter := range offering.Parameters {
			parameters[parameter.Name] = parameter
		}
		if conversion.Retired {
			// Never sent: it only names what old plans froze.
			continue
		}
		for name := range input {
			if _, declared := parameters[name]; !declared {
				return fmt.Errorf("conversions[%d].input 的参数 %q 未在 Offering %q 声明", index, name, conversion.OfferingID)
			}
		}
		if len(conversion.FormatValues) == 0 {
			continue
		}
		parameter, declared := parameters[conversion.FormatParameter]
		if !declared {
			return fmt.Errorf("conversions[%d].format_parameter %q 未在 Offering %q 声明", index, conversion.FormatParameter, conversion.OfferingID)
		}
		options := map[string]struct{}{}
		for _, option := range parameter.Options {
			options[option.Value] = struct{}{}
		}
		for format, value := range conversion.FormatValues {
			if _, exists := options[value]; !exists {
				return fmt.Errorf("conversions[%d].format_values[%s]=%q 不是 Offering %q 的选项", index, format, value, conversion.OfferingID)
			}
		}
	}
	return nil
}

// validateConvertedObjOfferings checks that every Offering named as producing
// converted OBJ files exists and is a format conversion.
func validateConvertedObjOfferings(
	delivery ModelDeliverySchema,
	offerings map[schemacontract.OfferingID]schemacontract.Offering,
) error {
	for _, id := range delivery.Archive.ObjPackage.ConvertedOfferings {
		offering, exists := offerings[schemacontract.OfferingID(id)]
		if !exists || offering.Capability != schemacontract.FormatConversion {
			return fmt.Errorf("archive.obj_package.converted_offerings 引用了未知或非格式转换的 Offering %q", id)
		}
	}
	return nil
}

// Matches reports whether a saved file of role, variant and media type is one
// this match selects (roles, then variants or variant prefixes, then media
// types; each list empty means any). Comparisons ignore case.
func (match ModelAssetMatch) Matches(role, variant, mediaType string) bool {
	if !slices.Contains(match.Roles, role) {
		return false
	}
	if len(match.Variants) > 0 || len(match.VariantPrefixes) > 0 {
		matched := slices.ContainsFunc(match.Variants, func(candidate string) bool { return strings.EqualFold(candidate, variant) })
		matched = matched || slices.ContainsFunc(match.VariantPrefixes, func(prefix string) bool {
			return prefix != "" && len(variant) >= len(prefix) && strings.EqualFold(variant[:len(prefix)], prefix)
		})
		if !matched {
			return false
		}
	}
	return len(match.MediaTypes) == 0 || slices.ContainsFunc(match.MediaTypes, func(candidate string) bool { return strings.EqualFold(candidate, mediaType) })
}

// FormatOf is the public format ID of a saved file (#326): the first format
// whose matches select it, else the format the archive's media type table
// names for its media type. A Provider's own result field names (such as
// rigged_character_glb_url) never reach a person or the Agent; a file of no
// known format has none.
// FormatLabelOf is the public name of that format ("GLB"), for a person to
// read where a file is listed (#327).
func (schema ModelDeliverySchema) FormatLabelOf(role, variant, mediaType string) (string, bool) {
	id, ok := schema.FormatOf(role, variant, mediaType)
	if !ok {
		return "", false
	}
	for _, format := range schema.Formats {
		if format.ID == id && format.Label != "" {
			return format.Label, true
		}
	}
	return "", false
}

func (schema ModelDeliverySchema) FormatOf(role, variant, mediaType string) (string, bool) {
	for _, format := range schema.Formats {
		for _, match := range format.Matches {
			if match.Matches(role, variant, mediaType) {
				return format.ID, true
			}
		}
	}
	extension := strings.TrimPrefix(schema.Archive.MediaTypeExtensions[strings.ToLower(mediaType)], ".")
	for _, format := range schema.Formats {
		if extension != "" && format.ID == extension {
			return format.ID, true
		}
	}
	return "", false
}
