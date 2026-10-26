package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type legacyParameterSchema struct {
	AdjustmentPolicy string
	AdjustmentReason string
	ReadOnly         bool
	Validation       *generation.Parameter
	Name             string
	Label            string
	Type             generation.PresentationControl
	Options          []generation.ParameterOption
	Default          json.RawMessage
	Required         bool
	RequiredSet      bool
	Minimum          *float64
	Maximum          *float64
	Step             *float64
	Description      string
	DisabledWhen     []generation.ParameterCondition
	VisibleWhen      []generation.ParameterCondition
	ForcedWhen       []generation.ForcedValue
	Notices          []generation.ParameterNotice
	// CostExtra 标记该参数参与额外积分计费（由 cost.extra_when 派生，
	// extended 投影专用；不泄漏触发条件本身）。
	CostExtra bool
	// Semantic 是参数语义（extended 投影专用，B7 语义判定）。
	Semantic string
}

type legacyInputParameterSchema struct {
	SameSession       bool
	TaskSourceBinding string
	ImageInput        *schema.ImageInputConstraint
	UploadedModel     *schema.UploadedModel
	TaskSourceKind    schema.TaskSourceKind
	TaskOfferings     []generation.OfferingID
	TaskCapabilities  []generation.Capability
	SameProvider      bool
	Name              string
	Label             string
	Type              string
	MediaTypes        []string
	Extensions        []string
	MaxBytes          int64
	MinItems          int
	MaxItems          int
}

type legacyModelConstraints struct {
	PromptMaxLength         *int
	NegativePromptMaxLength *int
	ReferenceImageMin       *int
	ReferenceImageMax       *int
	ReferenceImageFormats   []string
	ReferenceImageMaxSize   *int64
	PolycountMin            *float64
	PolycountMax            *float64
	OutputSizes             []generation.ParameterOption
}

func (constraints legacyModelConstraints) empty() bool {
	return constraints.PromptMaxLength == nil &&
		constraints.NegativePromptMaxLength == nil &&
		constraints.ReferenceImageMin == nil &&
		constraints.ReferenceImageMax == nil &&
		len(constraints.ReferenceImageFormats) == 0 &&
		constraints.ReferenceImageMaxSize == nil &&
		constraints.PolycountMin == nil &&
		constraints.PolycountMax == nil &&
		len(constraints.OutputSizes) == 0
}

type legacyProviderMeta struct {
	ID               string
	Name             string
	ProviderKey      string
	Capabilities     []generation.Capability
	Domain           generation.CapabilityDomain
	Features         []string
	ParamSchemas     map[generation.Capability][]legacyParameterSchema
	InputSchemas     map[generation.Capability][]legacyInputParameterSchema
	Constraints      map[generation.Capability]legacyModelConstraints
	Recommended      bool
	EstimatedCredits int
	NativeQuotes     map[generation.Capability]*generation.NativeQuote
	// EstimatedExtraCredits 是按冻结 cost 声明派生的额外积分估算
	//（extended 投影专用，§8.2 安全派生：只输出数字）。
	EstimatedExtraCredits int
	Requested             generation.Capability
}

type providerProjection struct {
	key       string
	offerings []generation.Offering
}

func (s *Server) legacyProviderDiscovery(
	writer http.ResponseWriter,
	request *http.Request,
	_ generation.OwnerID,
) {
	discovery := s.application.ProviderDiscovery
	configured := discovery.API
	capability := generation.Capability(
		strings.TrimSpace(
			request.URL.Query().Get(configured.Query.Capability),
		),
	)
	domain := generation.CapabilityDomain(
		strings.TrimSpace(request.URL.Query().Get(configured.Query.Domain)),
	)
	extended := request.URL.Query().Get(configured.Query.Projection) ==
		configured.ProjectionValues.Extended
	projections := groupProviderOfferings(s.generation.Offerings(""), discovery.LegacyModels)

	if capability != "" {
		providers, defaultModel := projectProvidersByCapability(
			projections,
			capability,
			s.application.ProviderDiscovery.LegacyConstraintOptionLabels,
		)
		writeJSON(writer, http.StatusOK, s.localizedSchemaData(request, map[string]any{
			configured.EnvelopeFields.Providers: projectProviderListWire(
				providers,
				discovery,
				extended,
			),
			configured.EnvelopeFields.DefaultModel: defaultModel,
		}))
		return
	}

	providers := projectProvidersByDomain(
		projections,
		domain,
		s.application.ProviderDiscovery.LegacyModels,
	)
	writeJSON(
		writer,
		http.StatusOK,
		s.localizedSchemaData(request, projectProviderListWire(providers, discovery, extended)),
	)
}

func groupProviderOfferings(
	offerings []generation.Offering,
	legacyModels []catalog.ProviderDiscoveryLegacyModel,
) []providerProjection {
	nativeCompat := map[string]bool{}
	for _, model := range legacyModels {
		nativeCompat[model.ModelID] = model.NativePricing
	}
	byKey := map[string]*providerProjection{}
	var ordered []*providerProjection
	for _, offering := range offerings {
		if offering.NativePricing != nil && !nativeCompat[offering.ModelID] {
			continue
		}
		key := offering.ProviderID + "\x00" + offering.ModelID
		projection := byKey[key]
		if projection == nil {
			projection = &providerProjection{key: key}
			byKey[key] = projection
			ordered = append(ordered, projection)
		}
		projection.offerings = append(projection.offerings, offering)
	}
	result := make([]providerProjection, 0, len(ordered))
	for _, projection := range ordered {
		sort.Slice(projection.offerings, func(left, right int) bool {
			return projection.offerings[left].CapabilityOrder <
				projection.offerings[right].CapabilityOrder
		})
		result = append(result, *projection)
	}
	return result
}

func projectProvidersByCapability(
	projections []providerProjection,
	capability generation.Capability,
	legacyConstraintOptionLabels map[string]map[string]string,
) ([]legacyProviderMeta, string) {
	type orderedProvider struct {
		order    int
		provider legacyProviderMeta
	}
	var selected []orderedProvider
	defaultModel := ""
	for _, projection := range projections {
		requested, exists := offeringForCapability(
			projection.offerings,
			capability,
		)
		if !exists {
			continue
		}
		provider := projectProvider(projection.offerings, requested)
		provider.Requested = capability
		constraints := projectConstraints(requested.Parameters)
		applyLegacyConstraintOptionLabels(
			constraints.OutputSizes,
			legacyConstraintOptionLabels[requested.ModelID],
		)
		if !constraints.empty() {
			provider.Constraints = map[generation.Capability]legacyModelConstraints{
				capability: constraints,
			}
		}
		if requested.Default {
			defaultModel = requested.ModelID
		}
		selected = append(selected, orderedProvider{
			order:    requested.DisplayOrder,
			provider: provider,
		})
	}
	sort.Slice(selected, func(left, right int) bool {
		if selected[left].order != selected[right].order {
			return selected[left].order < selected[right].order
		}
		return selected[left].provider.ID < selected[right].provider.ID
	})
	providers := make([]legacyProviderMeta, 0, len(selected))
	for _, item := range selected {
		providers = append(providers, item.provider)
	}
	return providers, defaultModel
}

func applyLegacyConstraintOptionLabels(
	options []generation.ParameterOption,
	labels map[string]string,
) {
	for index := range options {
		if label, exists := labels[options[index].Value]; exists {
			options[index].Label = label
		}
	}
}

func projectProvidersByDomain(
	projections []providerProjection,
	domain generation.CapabilityDomain,
	legacyModels []catalog.ProviderDiscoveryLegacyModel,
) []legacyProviderMeta {
	byModelID := make(map[string]legacyProviderMeta, len(projections))
	for _, projection := range projections {
		if len(projection.offerings) == 0 {
			continue
		}
		requested := projection.offerings[0]
		if domain != "" {
			var exists bool
			for _, offering := range projection.offerings {
				if offering.Domain == domain {
					requested = offering
					exists = true
					break
				}
			}
			if !exists {
				continue
			}
		}
		byModelID[requested.ModelID] = projectProvider(
			projection.offerings,
			requested,
		)
	}
	providers := make([]legacyProviderMeta, 0, len(byModelID))
	for _, configured := range legacyModels {
		provider, exists := byModelID[configured.ModelID]
		if !exists {
			continue
		}
		provider.ProviderKey = configured.ProviderKey
		providers = append(providers, provider)
	}
	return providers
}

func projectProvider(
	offerings []generation.Offering,
	requested generation.Offering,
) legacyProviderMeta {
	provider := legacyProviderMeta{
		ID:               requested.ModelID,
		Name:             requested.ModelName,
		Domain:           requested.Domain,
		Recommended:      requested.Recommended,
		EstimatedCredits: requested.EstimatedCredits,
		NativeQuotes:     map[generation.Capability]*generation.NativeQuote{},
		ParamSchemas:     map[generation.Capability][]legacyParameterSchema{},
		InputSchemas:     map[generation.Capability][]legacyInputParameterSchema{},
	}
	if requested.Cost != nil {
		provider.EstimatedExtraCredits = requested.Cost.ExtraCredits
	}
	for _, offering := range offerings {
		if offering.NativePricing != nil {
			quote, err := offering.DefaultNativeQuote(nil)
			if err == nil && quote != nil {
				provider.NativeQuotes[offering.Capability] = quote
			}
		}
		for _, feature := range offering.Features {
			if !slices.Contains(provider.Features, feature) {
				provider.Features = append(provider.Features, feature)
			}
		}
		provider.Capabilities = append(
			provider.Capabilities,
			offering.Capability,
		)
		schemas := projectParameterSchemas(offering.Parameters, offering.Cost)
		if len(schemas) > 0 {
			provider.ParamSchemas[offering.Capability] = schemas
		} else {
			provider.ParamSchemas[offering.Capability] = []legacyParameterSchema{}
		}
		inputSchemas := projectInputParameterSchemas(offering.Parameters)
		if len(inputSchemas) > 0 {
			provider.InputSchemas[offering.Capability] = inputSchemas
		}
	}
	return provider
}

func projectProviderListWire(
	providers []legacyProviderMeta,
	discovery catalog.ProviderDiscoverySchema,
	extended bool,
) []map[string]any {
	configured := make(
		map[string]catalog.ProviderDiscoveryLegacyModel,
		len(discovery.LegacyModels),
	)
	for _, model := range discovery.LegacyModels {
		configured[model.ModelID] = model
	}
	result := make([]map[string]any, 0, len(providers))
	for _, provider := range providers {
		if model, exists := configured[provider.ID]; exists {
			applyLegacyProviderIdentity(&provider, model)
			if !extended {
				applyLegacyParameterProjections(
					&provider,
					model.ParameterProjections,
				)
				applyLegacyConstraintProjections(
					&provider,
					model.ConstraintProjections,
				)
			}
		}
		result = append(
			result,
			projectProviderWire(provider, discovery.API, extended),
		)
	}
	return result
}

func applyLegacyProviderIdentity(
	provider *legacyProviderMeta,
	configured catalog.ProviderDiscoveryLegacyModel,
) {
	if configured.Name != "" {
		provider.Name = configured.Name
	}
	if configured.Recommended != nil {
		provider.Recommended = *configured.Recommended
	}
}

func applyLegacyParameterProjections(
	provider *legacyProviderMeta,
	projections map[generation.Capability]catalog.ProviderDiscoveryLegacyParameterProjection,
) {
	for capability, projection := range projections {
		schemas, exists := provider.ParamSchemas[capability]
		if !exists {
			continue
		}
		omitted := make(map[string]struct{}, len(projection.Omit))
		for _, parameter := range projection.Omit {
			omitted[parameter] = struct{}{}
		}
		projected := make([]legacyParameterSchema, 0, len(schemas))
		for _, schema := range schemas {
			if _, exists := omitted[schema.Name]; exists {
				continue
			}
			if fields, exists := projection.Fields[schema.Name]; exists {
				applyLegacyParameterFields(&schema, fields)
			}
			projected = append(projected, schema)
		}
		if len(projection.Order) > 0 {
			byName := make(
				map[string]legacyParameterSchema,
				len(projected),
			)
			for _, schema := range projected {
				byName[schema.Name] = schema
			}
			ordered := make(
				[]legacyParameterSchema,
				0,
				len(projection.Order),
			)
			for _, parameter := range projection.Order {
				if schema, exists := byName[parameter]; exists {
					ordered = append(ordered, schema)
				}
			}
			projected = ordered
		}
		provider.ParamSchemas[capability] = projected
	}
}

func applyLegacyParameterFields(
	parameter *legacyParameterSchema,
	configured catalog.ProviderDiscoveryLegacyParameterFieldSchema,
) {
	for _, field := range configured.Omit {
		switch field {
		case "options":
			parameter.Options = nil
		case "default":
			parameter.Default = nil
		case "required":
			parameter.Required = false
			parameter.RequiredSet = false
		case "minimum":
			parameter.Minimum = nil
		case "maximum":
			parameter.Maximum = nil
		case "step":
			parameter.Step = nil
		case "description":
			parameter.Description = ""
		}
	}
	if configured.Required != nil {
		parameter.Required = *configured.Required
		parameter.RequiredSet = true
	}
	if configured.Description != nil {
		parameter.Description = *configured.Description
	}
	if len(configured.Default) > 0 {
		parameter.Default = configured.Default
	}
}

func applyLegacyConstraintProjections(
	provider *legacyProviderMeta,
	projections map[generation.Capability]catalog.ProviderDiscoveryLegacyConstraintProjection,
) {
	if provider.Requested == "" {
		return
	}
	projection, exists := projections[provider.Requested]
	if !exists {
		return
	}
	if provider.Constraints == nil {
		provider.Constraints =
			map[generation.Capability]legacyModelConstraints{}
	}
	constraints := provider.Constraints[provider.Requested]
	for _, field := range projection.Omit {
		omitLegacyConstraintField(&constraints, field)
	}
	applyLegacyConstraintOverrides(
		&constraints,
		projection.Overrides,
	)
	if constraints.empty() {
		delete(provider.Constraints, provider.Requested)
	} else {
		provider.Constraints[provider.Requested] = constraints
	}
}

func omitLegacyConstraintField(
	constraints *legacyModelConstraints,
	field string,
) {
	switch field {
	case "prompt_max_length":
		constraints.PromptMaxLength = nil
	case "negative_prompt_max_length":
		constraints.NegativePromptMaxLength = nil
	case "reference_image_min":
		constraints.ReferenceImageMin = nil
	case "reference_image_max":
		constraints.ReferenceImageMax = nil
	case "reference_image_formats":
		constraints.ReferenceImageFormats = nil
	case "reference_image_max_size":
		constraints.ReferenceImageMaxSize = nil
	case "polycount_min":
		constraints.PolycountMin = nil
	case "polycount_max":
		constraints.PolycountMax = nil
	case "output_sizes":
		constraints.OutputSizes = nil
	}
}

func applyLegacyConstraintOverrides(
	constraints *legacyModelConstraints,
	overrides catalog.ProviderDiscoveryLegacyConstraintOverrideSchema,
) {
	if overrides.PromptMaxLength != nil {
		value := *overrides.PromptMaxLength
		constraints.PromptMaxLength = &value
	}
	if overrides.NegativePromptMaxLength != nil {
		value := *overrides.NegativePromptMaxLength
		constraints.NegativePromptMaxLength = &value
	}
	if overrides.ReferenceImageMin != nil {
		value := *overrides.ReferenceImageMin
		constraints.ReferenceImageMin = &value
	}
	if overrides.ReferenceImageMax != nil {
		value := *overrides.ReferenceImageMax
		constraints.ReferenceImageMax = &value
	}
	if overrides.ReferenceImageMaxSize != nil {
		value := *overrides.ReferenceImageMaxSize
		constraints.ReferenceImageMaxSize = &value
	}
	if overrides.PolycountMin != nil {
		value := *overrides.PolycountMin
		constraints.PolycountMin = &value
	}
	if overrides.PolycountMax != nil {
		value := *overrides.PolycountMax
		constraints.PolycountMax = &value
	}
}

func projectProviderWire(
	provider legacyProviderMeta,
	api catalog.ProviderDiscoveryAPISchema,
	extended bool,
) map[string]any {
	fields := api.ProviderFields
	result := map[string]any{
		fields.ID:           provider.ID,
		fields.Name:         provider.Name,
		fields.Capabilities: provider.Capabilities,
		fields.Domain:       provider.Domain,
	}
	if provider.ProviderKey != "" {
		result[fields.ProviderKey] = provider.ProviderKey
	}
	if extended {
		if len(provider.NativeQuotes) > 0 {
			if fields.NativeQuotes != "" {
				result[fields.NativeQuotes] = provider.NativeQuotes
			}
		}
		// A list or domain projection has no single requested capability. If
		// any capability has native pricing, a model-wide legacy integer would
		// be ambiguous (and often the zero value), so only emit it when a
		// specifically requested capability is legacy-priced.
		if len(provider.NativeQuotes) == 0 || provider.Requested != "" && provider.NativeQuotes[provider.Requested] == nil {
			result[fields.EstimatedCredits] = provider.EstimatedCredits
		}
		if provider.EstimatedExtraCredits > 0 {
			result[fields.EstimatedExtraCredits] = provider.EstimatedExtraCredits
		}
	}
	if extended && len(provider.Features) > 0 {
		result[fields.Features] = provider.Features
	}
	if len(provider.ParamSchemas) > 0 {
		projected := make(map[string]any, len(provider.ParamSchemas))
		for capability, schemas := range provider.ParamSchemas {
			items := make([]map[string]any, 0, len(schemas))
			for _, schema := range schemas {
				if schema.ReadOnly && !extended {
					continue
				}
				items = append(
					items,
					projectParameterSchemaWire(schema, api, extended),
				)
			}
			projected[string(capability)] = items
		}
		result[fields.ParameterSchemas] = projected
	}
	if extended && len(provider.InputSchemas) > 0 {
		projected := make(map[string]any, len(provider.InputSchemas))
		for capability, schemas := range provider.InputSchemas {
			items := make([]map[string]any, 0, len(schemas))
			for _, schema := range schemas {
				items = append(
					items,
					projectInputSchemaWire(schema, api),
				)
			}
			projected[string(capability)] = items
		}
		result[fields.InputSchemas] = projected
	}
	if len(provider.Constraints) > 0 {
		projected := make(map[string]any, len(provider.Constraints))
		for capability, constraints := range provider.Constraints {
			projected[string(capability)] =
				projectConstraintWire(constraints, api)
		}
		result[fields.Constraints] = projected
	}
	if provider.Recommended {
		result[fields.Recommended] = true
	}
	return result
}

func projectParameterSchemaWire(
	parameter legacyParameterSchema,
	api catalog.ProviderDiscoveryAPISchema,
	extended bool,
) map[string]any {
	fields := api.ParameterFields
	result := map[string]any{
		fields.Name:  parameter.Name,
		fields.Label: parameter.Label,
		fields.Type:  parameter.Type,
	}
	if extended && parameter.AdjustmentPolicy != "" {
		if fields.AdjustmentPolicy != "" {
			result[fields.AdjustmentPolicy] = parameter.AdjustmentPolicy
		}
		if fields.AdjustmentReason != "" {
			result[fields.AdjustmentReason] = parameter.AdjustmentReason
		}
		if fields.ReadOnly != "" {
			result[fields.ReadOnly] = parameter.ReadOnly
		}
	}
	if extended && fields.Validation != "" && parameter.Validation != nil {
		result[fields.Validation] = parameter.Validation
	}
	if len(parameter.Options) > 0 {
		result[fields.Options] = projectParameterOptionsWire(
			parameter.Options,
			api,
		)
	}
	if len(parameter.Default) > 0 {
		result[fields.Default] = parameter.Default
	}
	if parameter.Required || parameter.RequiredSet {
		result[fields.Required] = parameter.Required
	}
	if parameter.Minimum != nil {
		result[fields.Minimum] = *parameter.Minimum
	}
	if parameter.Maximum != nil {
		result[fields.Maximum] = *parameter.Maximum
	}
	if parameter.Step != nil {
		result[fields.Step] = *parameter.Step
	}
	if parameter.Description != "" {
		result[fields.Description] = parameter.Description
	}
	if extended && parameter.CostExtra && fields.CostExtra != "" {
		result[fields.CostExtra] = true
	}
	if extended && parameter.Semantic != "" && fields.Semantic != "" {
		result[fields.Semantic] = parameter.Semantic
	}
	if extended && len(parameter.DisabledWhen) > 0 {
		items := make([]map[string]any, 0, len(parameter.DisabledWhen))
		for _, condition := range parameter.DisabledWhen {
			items = append(items, projectParameterConditionWire(
				condition,
				api,
			))
		}
		result[fields.DisabledWhen] = items
	}
	if extended && len(parameter.VisibleWhen) > 0 {
		items := make([]map[string]any, 0, len(parameter.VisibleWhen))
		for _, condition := range parameter.VisibleWhen {
			items = append(items, projectParameterConditionWire(
				condition,
				api,
			))
		}
		result[fields.VisibleWhen] = items
	}
	if extended && len(parameter.ForcedWhen) > 0 {
		items := make([]map[string]any, 0, len(parameter.ForcedWhen))
		for _, forced := range parameter.ForcedWhen {
			item := map[string]any{
				api.ForcedValueFields.When: projectParameterConditionWire(
					forced.When,
					api,
				),
				api.ForcedValueFields.Value: json.RawMessage(forced.Value),
			}
			items = append(items, item)
		}
		result[fields.ForcedWhen] = items
	}
	if extended && len(parameter.Notices) > 0 {
		items := make([]map[string]any, 0, len(parameter.Notices))
		for _, notice := range parameter.Notices {
			noticeFields := api.NoticeFields
			items = append(items, map[string]any{
				noticeFields.When: projectParameterConditionWire(
					notice.When,
					api,
				),
				noticeFields.MessageKey: notice.MessageKey,
				noticeFields.Tone:       notice.Tone,
			})
		}
		result[fields.Notices] = items
	}
	return result
}

func projectParameterOptionsWire(
	options []generation.ParameterOption,
	api catalog.ProviderDiscoveryAPISchema,
) []map[string]any {
	fields := api.OptionFields
	result := make([]map[string]any, 0, len(options))
	for _, option := range options {
		item := map[string]any{
			fields.Value: option.Value,
			fields.Label: option.Label,
		}
		if option.Group != "" {
			item[fields.Group] = option.Group
		}
		result = append(result, item)
	}
	return result
}

func projectParameterConditionWire(
	condition generation.ParameterCondition,
	api catalog.ProviderDiscoveryAPISchema,
) map[string]any {
	fields := api.ConditionFields
	result := map[string]any{
		fields.Parameter: condition.Parameter,
		fields.Operator:  condition.Operator,
	}
	if len(condition.Value) > 0 {
		result[fields.Value] = condition.Value
	}
	return result
}

func projectInputSchemaWire(
	input legacyInputParameterSchema,
	api catalog.ProviderDiscoveryAPISchema,
) map[string]any {
	fields := api.InputFields
	result := map[string]any{
		fields.Name:  input.Name,
		fields.Label: input.Label,
		fields.Type:  input.Type,
	}
	if len(input.MediaTypes) > 0 {
		result[fields.MediaTypes] = input.MediaTypes
	}
	if len(input.Extensions) > 0 {
		result[fields.Extensions] = input.Extensions
	}
	if input.MaxBytes > 0 {
		result[fields.MaxBytes] = input.MaxBytes
	}
	if input.MinItems > 0 {
		result[fields.MinItems] = input.MinItems
	}
	if input.MaxItems > 0 {
		result[fields.MaxItems] = input.MaxItems
	}
	if input.UploadedModel != nil && fields.UploadedModel != "" {
		result[fields.UploadedModel] = input.UploadedModel
	}
	if input.ImageInput != nil && fields.ImageInput != "" {
		result[fields.ImageInput] = input.ImageInput
	}
	if input.SameSession && fields.SameSession != "" {
		result[fields.SameSession] = true
	}
	if input.TaskSourceBinding != "" && fields.TaskSourceBinding != "" {
		result[fields.TaskSourceBinding] = input.TaskSourceBinding
	}
	if input.TaskSourceKind != "" || input.SameSession || input.TaskSourceBinding != "" {
		if fields.TaskSourceKind != "" {
			result[fields.TaskSourceKind] = input.TaskSourceKind
		}
		if len(input.TaskOfferings) > 0 && fields.TaskOfferings != "" {
			result[fields.TaskOfferings] = input.TaskOfferings
		}
		if len(input.TaskCapabilities) > 0 && fields.TaskCapabilities != "" {
			result[fields.TaskCapabilities] = input.TaskCapabilities
		}
		if fields.SameProvider != "" {
			result[fields.SameProvider] = input.SameProvider
		}
	}
	return result
}

func projectConstraintWire(
	constraints legacyModelConstraints,
	api catalog.ProviderDiscoveryAPISchema,
) map[string]any {
	fields := api.ConstraintFields
	result := map[string]any{}
	if constraints.PromptMaxLength != nil {
		result[fields.PromptMaxLength] = *constraints.PromptMaxLength
	}
	if constraints.NegativePromptMaxLength != nil {
		result[fields.NegativePromptMaxLength] =
			*constraints.NegativePromptMaxLength
	}
	if constraints.ReferenceImageMin != nil {
		result[fields.ReferenceImageMin] = *constraints.ReferenceImageMin
	}
	if constraints.ReferenceImageMax != nil {
		result[fields.ReferenceImageMax] = *constraints.ReferenceImageMax
	}
	if len(constraints.ReferenceImageFormats) > 0 {
		result[fields.ReferenceImageFormats] =
			constraints.ReferenceImageFormats
	}
	if constraints.ReferenceImageMaxSize != nil {
		result[fields.ReferenceImageMaxSize] =
			*constraints.ReferenceImageMaxSize
	}
	if constraints.PolycountMin != nil {
		result[fields.PolycountMin] = *constraints.PolycountMin
	}
	if constraints.PolycountMax != nil {
		result[fields.PolycountMax] = *constraints.PolycountMax
	}
	if len(constraints.OutputSizes) > 0 {
		result[fields.OutputSizes] = projectParameterOptionsWire(
			constraints.OutputSizes,
			api,
		)
	}
	return result
}

func offeringForCapability(
	offerings []generation.Offering,
	capability generation.Capability,
) (generation.Offering, bool) {
	for _, offering := range offerings {
		if offering.Capability == capability {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

func projectParameterSchemas(
	parameters []generation.Parameter,
	cost *generation.CostDeclaration,
) []legacyParameterSchema {
	extraParameters := map[string]bool{}
	if cost != nil {
		for _, condition := range cost.ExtraWhen {
			extraParameters[condition.Parameter] = true
		}
		// 多选形态（W6）：selection_parameter 指向的参数按选中项计费，
		// 同样标记为 cost-bearing（UI 由此渲染估算与同意门槛）。
		if cost.SelectionParameter != "" {
			extraParameters[cost.SelectionParameter] = true
		}
	}
	var schemas []legacyParameterSchema
	for _, parameter := range parameters {
		presentation := parameter.Presentation
		// 兼容表单投影不支持只读行：task、hidden 和 fixed 参数不进 wire；
		// canvas 等展示语义保持投影，线形状对 legacy 消费者稳定。
		if presentation == nil || parameter.Fixed ||
			(presentation.Surface == generation.SurfaceReadOnly && parameter.AdjustmentPolicy != "advised") ||
			presentation.Surface == generation.SurfaceTask ||
			presentation.Surface == generation.SurfaceHidden {
			continue
		}
		schemas = append(schemas, legacyParameterSchema{
			AdjustmentPolicy: parameter.AdjustmentPolicy, AdjustmentReason: parameter.AdjustmentReason, ReadOnly: parameter.AdjustmentPolicy == "advised",
			Validation:   &generation.Parameter{Type: parameter.Type, MaxLength: parameter.MaxLength, MinItems: parameter.MinItems, MaxItems: parameter.MaxItems, MaxBytes: parameter.MaxBytes, MediaTypes: parameter.MediaTypes, ImageSize: parameter.ImageSize, ImageInput: parameter.ImageInput, Integer: parameter.Integer},
			Name:         parameter.Name,
			Label:        parameter.Label,
			Type:         presentation.Control,
			Options:      append([]generation.ParameterOption(nil), parameter.Options...),
			Default:      presentationDefault(parameter),
			Required:     parameter.Required,
			Minimum:      parameter.Minimum,
			Maximum:      parameter.Maximum,
			Step:         presentation.Step,
			Description:  parameter.Description,
			DisabledWhen: cloneParameterConditions(presentation.DisabledWhen),
			VisibleWhen:  cloneParameterConditions(presentation.VisibleWhen),
			ForcedWhen:   cloneForcedValues(parameter.ForcedWhen),
			Notices:      cloneParameterNotices(presentation.Notices),
			CostExtra:    extraParameters[parameter.Name],
			Semantic:     string(parameter.Semantic),
		})
	}
	return schemas
}

func projectInputParameterSchemas(
	parameters []generation.Parameter,
) []legacyInputParameterSchema {
	var schemas []legacyInputParameterSchema
	for _, parameter := range parameters {
		if parameter.Type != "asset" && parameter.Type != "asset-array" &&
			!(parameter.Type == "task" && (parameter.TaskSourceKind == schema.TaskSourceAssets || parameter.SameSession || parameter.TaskSourceBinding != "")) {
			continue
		}
		var uploadedModel *schema.UploadedModel
		var imageInput *schema.ImageInputConstraint
		if parameter.ImageInput != nil {
			value := *parameter.ImageInput
			imageInput = &value
		}
		if parameter.UploadedModel != nil {
			value := *parameter.UploadedModel
			if value.ProfileLimits != nil {
				limits := *value.ProfileLimits
				value.ProfileLimits = &limits
			}
			uploadedModel = &value
		}
		schemas = append(schemas, legacyInputParameterSchema{
			ImageInput:     imageInput,
			UploadedModel:  uploadedModel,
			TaskSourceKind: parameter.TaskSourceKind, SameSession: parameter.SameSession, TaskSourceBinding: parameter.TaskSourceBinding,
			TaskOfferings:    append([]generation.OfferingID(nil), parameter.TaskOfferings...),
			TaskCapabilities: append([]generation.Capability(nil), parameter.TaskCapabilities...),
			SameProvider:     parameter.SameProvider,
			Name:             parameter.Name,
			Label:            parameter.Label,
			Type:             parameter.Type,
			MediaTypes:       append([]string(nil), parameter.MediaTypes...),
			Extensions:       append([]string(nil), parameter.Extensions...),
			MaxBytes:         parameter.MaxBytes,
			MinItems:         parameter.MinItems,
			MaxItems:         parameter.MaxItems,
		})
	}
	return schemas
}

func cloneParameterConditions(
	source []generation.ParameterCondition,
) []generation.ParameterCondition {
	result := make([]generation.ParameterCondition, len(source))
	for index, condition := range source {
		result[index] = condition
		result[index].Value = append(json.RawMessage(nil), condition.Value...)
	}
	return result
}

func cloneForcedValues(
	source []generation.ForcedValue,
) []generation.ForcedValue {
	result := make([]generation.ForcedValue, len(source))
	for index, forced := range source {
		result[index] = forced
		result[index].When.Value = append(
			json.RawMessage(nil),
			forced.When.Value...,
		)
		result[index].Value = append(json.RawMessage(nil), forced.Value...)
	}
	return result
}

func cloneParameterNotices(
	source []generation.ParameterNotice,
) []generation.ParameterNotice {
	result := make([]generation.ParameterNotice, len(source))
	for index, notice := range source {
		result[index] = notice
		result[index].When.Value = append(
			json.RawMessage(nil),
			notice.When.Value...,
		)
	}
	return result
}

func presentationDefault(parameter generation.Parameter) json.RawMessage {
	if len(parameter.Default) == 0 {
		return nil
	}
	if parameter.Type != "string-array" ||
		parameter.Presentation == nil ||
		parameter.Presentation.Control != generation.ControlSelect {
		return append(json.RawMessage(nil), parameter.Default...)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(parameter.Default, &values); err != nil ||
		len(values) == 0 {
		return append(json.RawMessage(nil), parameter.Default...)
	}
	return append(json.RawMessage(nil), values[0]...)
}

func projectConstraints(
	parameters []generation.Parameter,
) legacyModelConstraints {
	var constraints legacyModelConstraints
	for _, parameter := range parameters {
		switch parameter.Semantic {
		case generation.SemanticPrompt:
			if parameter.MaxLength > 0 {
				value := parameter.MaxLength
				constraints.PromptMaxLength = &value
			}
		case generation.SemanticNegativePrompt:
			if parameter.MaxLength > 0 {
				value := parameter.MaxLength
				constraints.NegativePromptMaxLength = &value
			}
		case generation.SemanticReferenceImage:
			if parameter.MinItems > 0 {
				value := parameter.MinItems
				constraints.ReferenceImageMin = &value
			}
			if parameter.MaxItems > 0 {
				value := parameter.MaxItems
				constraints.ReferenceImageMax = &value
			}
			constraints.ReferenceImageFormats = append(
				[]string(nil),
				parameter.Extensions...,
			)
			if parameter.MaxBytes > 0 {
				value := parameter.MaxBytes
				constraints.ReferenceImageMaxSize = &value
			}
		case generation.SemanticPolycount:
			constraints.PolycountMin = parameter.Minimum
			constraints.PolycountMax = parameter.Maximum
		case generation.SemanticOutputSize:
			constraints.OutputSizes = append(
				[]generation.ParameterOption(nil),
				parameter.Options...,
			)
		}
	}
	return constraints
}
