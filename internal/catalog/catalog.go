package catalog

// Catalog loading and public lookup entry points.
import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"io"
	"sort"
	"strings"
	"time"
)

//go:embed default_catalog.json
var defaultCatalog []byte

func LoadDefault() (*Catalog, error) {
	return Load(bytes.NewReader(defaultCatalog))
}

func Load(reader io.Reader) (*Catalog, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var doc document
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("解析 Offering Catalog: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("不支持 Catalog 版本 %d", doc.Version)
	}
	if doc.Application == nil {
		return nil, errors.New("Catalog 缺少 application Product Schema")
	}
	if err := expandSourceModelLimitTexts(&doc); err != nil {
		return nil, err
	}
	if err := validateApplication(*doc.Application); err != nil {
		return nil, fmt.Errorf("application: %w", err)
	}

	result := &Catalog{
		application: cloneApplication(*doc.Application),
		byID:        make(generationMap, len(doc.Offerings)),
	}
	triples := map[string]schemacontract.OfferingID{}
	providerCreditsMetered := map[string]creditsMeteredDeclaration{}
	providerShortLabels := map[string]string{}
	providerOrders := map[string]int{}
	orderProviders := map[int]string{}
	defaults := map[schemacontract.Capability]schemacontract.OfferingID{}
	displayOrders := map[schemacontract.Capability]map[int]schemacontract.OfferingID{}
	capabilityOrders := map[string]map[int]schemacontract.OfferingID{}
	applicationCapabilities := make(
		map[schemacontract.Capability]schemacontract.CapabilityDomain,
		len(doc.Application.Capabilities),
	)
	for _, capability := range doc.Application.Capabilities {
		applicationCapabilities[capability.ID] = capability.Domain
	}
	for index, raw := range doc.Offerings {
		if raw.DisplayOrder == nil {
			return nil, fmt.Errorf("offerings[%d]: 缺少 display_order", index)
		}
		if raw.CapabilityOrder == nil {
			return nil, fmt.Errorf("offerings[%d]: 缺少 capability_order", index)
		}
		offering := raw.toOffering()
		offering.ExecutionMessages = doc.Application.Runtime.GenerationMessages
		offering.Cancellation = doc.Application.Runtime.TaskCancellation
		offering.Cancellation.Supported = raw.CancellationSupported
		if err := freezeOfferingModelProfiles(&offering, *doc.Application); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		if err := freezeLocalConversionProfile(&offering, *doc.Application); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		if err := validateOffering(
			offering,
			ProtocolByAdapterKey(doc.Application.Runtime.ProviderConnections),
			ConnectionCapabilitiesByAdapterKey(doc.Application.Runtime.ProviderConnections),
		); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		if err := validateImageResultGroupOffering(*doc.Application, offering); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		if err := validateUploadedModelParameters(offering, doc.Application.Runtime.InputAssets.ModelProfile); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		if err := validateGeneratedReferenceCoverage(offering, doc.Application.Runtime.InputAssets); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		if err := validateOfferingContent(offering, doc.Application.Content); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		if err := validateOfferingTerminology(doc.Application.Terminology, offering); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		if err := validateCanvasRatioCoverage(offering, doc.Application.Canvas.Frontend.Artboard); err != nil {
			return nil, fmt.Errorf("offerings[%d]: %w", index, err)
		}
		capabilityDomain, exists := applicationCapabilities[offering.Capability]
		if !exists {
			return nil, fmt.Errorf(
				"offerings[%d]: 能力 %q 未在 application.capabilities 声明",
				index,
				offering.Capability,
			)
		}
		if offering.Domain != capabilityDomain {
			return nil, fmt.Errorf(
				"offerings[%d]: 能力 %q 的 domain %q 与 application.capabilities 声明 %q 不一致",
				index,
				offering.Capability,
				offering.Domain,
				capabilityDomain,
			)
		}
		if _, exists := result.byID[offering.ID]; exists {
			return nil, fmt.Errorf("Offering ID 重复: %q", offering.ID)
		}
		if previous, exists := providerCreditsMetered[offering.ProviderID]; exists {
			if previous.metered != offering.CreditsMetered {
				return nil, fmt.Errorf(
					"Provider %q 的 credits_metered 不一致: Offering %q 与 %q",
					offering.ProviderID,
					previous.offeringID,
					offering.ID,
				)
			}
		} else {
			providerCreditsMetered[offering.ProviderID] = creditsMeteredDeclaration{
				metered:    offering.CreditsMetered,
				offeringID: offering.ID,
			}
		}
		if previous, exists := providerShortLabels[offering.ProviderID]; exists && previous != offering.ProviderShortLabel {
			return nil, fmt.Errorf("Provider %q 的 provider_short_label 不一致: %q 与 %q", offering.ProviderID, previous, offering.ProviderShortLabel)
		}
		providerShortLabels[offering.ProviderID] = offering.ProviderShortLabel
		if offering.ProviderOrder < 0 {
			return nil, fmt.Errorf("Offering %q 的 provider_order 不能为负数", offering.ID)
		}
		if offering.ProviderOrder > 0 {
			if previous, exists := providerOrders[offering.ProviderID]; exists && previous != offering.ProviderOrder {
				return nil, fmt.Errorf("Provider %q 的 provider_order 不一致: %d 与 %d", offering.ProviderID, previous, offering.ProviderOrder)
			}
			if owner, exists := orderProviders[offering.ProviderOrder]; exists && owner != offering.ProviderID {
				return nil, fmt.Errorf("provider_order %d 被 Provider %q 与 %q 同时使用", offering.ProviderOrder, owner, offering.ProviderID)
			}
			providerOrders[offering.ProviderID] = offering.ProviderOrder
			orderProviders[offering.ProviderOrder] = offering.ProviderID
		}
		triple := strings.Join(
			[]string{offering.ProviderID, offering.ModelID, string(offering.Capability)},
			"\x00",
		)
		if previous, exists := triples[triple]; exists {
			return nil, fmt.Errorf("Offering 三元组重复: %q 与 %q", previous, offering.ID)
		}
		if offering.Default {
			if previous, exists := defaults[offering.Capability]; exists {
				return nil, fmt.Errorf(
					"能力 %q 的默认 Offering 重复: %q 与 %q",
					offering.Capability,
					previous,
					offering.ID,
				)
			}
			defaults[offering.Capability] = offering.ID
		}
		orders := displayOrders[offering.Capability]
		if orders == nil {
			orders = map[int]schemacontract.OfferingID{}
			displayOrders[offering.Capability] = orders
		}
		if previous, exists := orders[offering.DisplayOrder]; exists {
			return nil, fmt.Errorf(
				"能力 %q 的 display_order %d 重复: %q 与 %q",
				offering.Capability,
				offering.DisplayOrder,
				previous,
				offering.ID,
			)
		}
		orders[offering.DisplayOrder] = offering.ID
		modelKey := strings.Join(
			[]string{offering.ProviderID, offering.ModelID},
			"\x00",
		)
		modelOrders := capabilityOrders[modelKey]
		if modelOrders == nil {
			modelOrders = map[int]schemacontract.OfferingID{}
			capabilityOrders[modelKey] = modelOrders
		}
		if previous, exists := modelOrders[offering.CapabilityOrder]; exists {
			return nil, fmt.Errorf(
				"模型 %q 的 capability_order %d 重复: %q 与 %q",
				offering.ModelID,
				offering.CapabilityOrder,
				previous,
				offering.ID,
			)
		}
		modelOrders[offering.CapabilityOrder] = offering.ID
		triples[triple] = offering.ID
		result.byID[offering.ID] = offering
	}
	if len(result.byID) == 0 {
		return nil, errors.New("Catalog 至少需要一个 Offering")
	}
	// One declaration per provider is enough: its other Offerings inherit the rank.
	for id, offering := range result.byID {
		if order, declared := providerOrders[offering.ProviderID]; declared && offering.ProviderOrder == 0 {
			offering.ProviderOrder = order
			result.byID[id] = offering
		}
	}
	if err := validateTaskOfferingReferences(result.byID); err != nil {
		return nil, err
	}
	if err := validateTaskSourceDiscovery(result.application.ProviderDiscovery, result.byID); err != nil {
		return nil, err
	}
	if creation := result.application.Chat.Skills.Creation; creation.Route != "" {
		defaultSkill := SkillSchema{
			ID:              "creation-default-validation",
			Name:            "Creation default",
			RecommendedSize: creation.RecommendedSize,
			Prompt:          "validation",
		}
		if err := ValidateSkillCatalogAgainstProductSchema(
			SkillCatalogSchema{Revision: "creation-default-validation", Skills: []SkillSchema{defaultSkill}},
			result.application, result.List(""),
		); err != nil {
			return nil, fmt.Errorf("application.chat.skills.creation.recommended_size: %w", err)
		}
	}
	if err := validateSkillRecipes(result.application.Chat.Skills.Recipes); err != nil {
		return nil, fmt.Errorf("application.chat.skills.recipes: %w", err)
	}
	if err := validateLegacyThumbnailOfferingMappings(
		result.application.Runtime.LegacyMigration.
			ThumbnailBackfill.OfferingMappings,
		result.byID,
	); err != nil {
		return nil, fmt.Errorf(
			"application.runtime.legacy_migration.thumbnail_backfill: %w",
			err,
		)
	}
	if err := validateNativeBalanceUnits(result.application.Runtime, result.byID); err != nil {
		return nil, err
	}
	if err := validateChatToolOfferingSelection(result.application.Chat.Tools, result.byID); err != nil {
		return nil, fmt.Errorf("application.chat: %w", err)
	}
	if err := validateProviderDiscoveryLegacyModels(
		result.application.ProviderDiscovery.LegacyModels,
		result.byID,
	); err != nil {
		return nil, fmt.Errorf("application.provider_discovery: %w", err)
	}
	result.application.Runtime.AssetDelivery.Proxy.Security.AllowedHosts =
		assetHostsFromOfferings(result.byID)
	if err := validateLegacyOfferingScopes(result.application, result.byID); err != nil {
		return nil, err
	}
	if err := validateLegacyTaskAliasTargets(result.application, result.byID); err != nil {
		return nil, err
	}
	if err := validateLegacyFieldTargets(result.application, result.byID); err != nil {
		return nil, err
	}
	if err := validateLegacyMultipartTargets(result.application, result.byID); err != nil {
		return nil, err
	}
	if err := validateLegacyResultAssetTargets(result.application, result.byID); err != nil {
		return nil, err
	}
	if err := validateProviderDeprecations(result.application.ProviderDeprecations, result.byID); err != nil {
		return nil, err
	}
	if err := validateRetiredGenerationModels(result.application.Runtime.Frontend.RetiredGenerationModels, result.byID); err != nil {
		return nil, err
	}
	if err := validateRetiredModelTexts(result.application); err != nil {
		return nil, err
	}
	if err := validateModelDeliveryOfferings(result.application.ModelDelivery, result.byID); err != nil {
		return nil, fmt.Errorf("application.model_delivery: %w", err)
	}
	if err := validateConvertedObjOfferings(result.application.ModelDelivery, result.byID); err != nil {
		return nil, fmt.Errorf("application.model_delivery: %w", err)
	}
	// Translations are checked once the offerings themselves are valid, so a
	// broken offering reports its own error rather than a stale translation.
	if err := validateDocumentOfferingLocalization(doc); err != nil {
		return nil, fmt.Errorf("application.localization: %w", err)
	}
	if err := validateLocalizedOfferingTerminology(doc.Application.Terminology, doc.Application.Localization); err != nil {
		return nil, fmt.Errorf("application.localization: %w", err)
	}
	return result, nil
}

func (c *Catalog) DefaultCancellationPolicy() schemacontract.CancellationPolicy {
	return c.application.Runtime.TaskCancellation
}

func (c *Catalog) Application() ApplicationSchema {
	return cloneApplication(c.application)
}

func (raw rawOffering) toOffering() schemacontract.Offering {
	return schemacontract.Offering{
		ID:                            raw.ID,
		Revision:                      raw.Revision,
		ProviderID:                    raw.ProviderID,
		ProviderName:                  raw.ProviderName,
		ProviderShortLabel:            raw.ProviderShortLabel,
		ProviderOrder:                 valueOrZero(raw.ProviderOrder),
		ModelID:                       raw.ModelID,
		ModelName:                     raw.ModelName,
		Capability:                    raw.Capability,
		Domain:                        raw.Domain,
		AdapterKey:                    raw.AdapterKey,
		Enabled:                       raw.Enabled,
		Features:                      append([]string(nil), raw.Features...),
		Default:                       raw.Default,
		Recommended:                   raw.Recommended,
		DisplayOrder:                  *raw.DisplayOrder,
		CapabilityOrder:               *raw.CapabilityOrder,
		Priority:                      raw.Priority,
		EstimatedCredits:              raw.EstimatedCredits,
		NativePricing:                 raw.NativePricing,
		CreditsMetered:                raw.CreditsMetered,
		EstimatedCreditsUnitParameter: raw.EstimatedCreditsUnitParameter,
		ReferenceSelectionPolicy:      raw.ReferenceSelectionPolicy,
		IdempotentSubmission:          raw.IdempotentSubmission,
		Timeout:                       time.Duration(raw.TimeoutSeconds) * time.Second,
		JobTimeout:                    time.Duration(raw.JobTimeoutSeconds) * time.Second,
		PollInterval:                  time.Duration(raw.PollIntervalSeconds) * time.Second,
		Retry: schemacontract.RetryPolicy{
			MaxAttempts:    raw.Retry.MaxAttempts,
			InitialBackoff: time.Duration(raw.Retry.InitialBackoffSeconds) * time.Second,
			MaxBackoff:     time.Duration(raw.Retry.MaxBackoffSeconds) * time.Second,
			Retryable:      append([]schemacontract.FailureClass(nil), raw.Retry.Retryable...),
		},
		MaterializeAttempts: raw.MaterializeAttempts,
		Parameters:          cloneParametersWithModelProfiles(raw.Parameters),
		InputRules:          cloneInputRules(raw.InputRules),
		Outputs:             append([]schemacontract.AssetRequirement(nil), raw.Outputs...),
		AssetHosts:          append([]string(nil), raw.AssetHosts...),
		ProviderOptions:     append(json.RawMessage(nil), raw.ProviderOptions...),
		SourceModelLimit:    cloneSourceModelLimit(raw.SourceModelLimit),
		OutputTriangles:     cloneOutputTriangles(raw.OutputTriangles),
		Cost:                cloneCostDeclaration(raw.Cost),
		CostPolicy:          cloneCostPolicy(raw.CostPolicy),
		PostSuccess:         clonePostSuccessPolicy(raw.PostSuccess),
	}
}

func (c *Catalog) Get(id schemacontract.OfferingID) (schemacontract.Offering, bool) {
	offering, ok := c.byID[id]
	return cloneOfferingModelProfiles(offering), ok
}

func (c *Catalog) List(capability schemacontract.Capability) []schemacontract.Offering {
	result := make([]schemacontract.Offering, 0)
	for _, offering := range c.byID {
		if !offering.Enabled {
			continue
		}
		if capability != "" && offering.Capability != capability {
			continue
		}
		result = append(result, cloneOfferingModelProfiles(offering))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority
		}
		if result[i].Recommended != result[j].Recommended {
			return result[i].Recommended
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (c *Catalog) Normalize(
	capability schemacontract.Capability,
	offeringID schemacontract.OfferingID,
	input json.RawMessage,
) (schemacontract.Offering, json.RawMessage, error) {
	offering, err := c.selectOffering(capability, offeringID)
	if err != nil {
		return schemacontract.Offering{}, nil, err
	}
	normalized, err := normalizeInput(offering, input)
	if err != nil {
		return schemacontract.Offering{}, nil, &schemacontract.Error{
			Code:      schemacontract.ErrInvalidInput,
			Operation: "normalize_input",
			Detail:    err.Error(),
			Cause:     err,
		}
	}
	return cloneOfferingModelProfiles(offering), normalized, nil
}

func (c *Catalog) selectOffering(
	capability schemacontract.Capability,
	offeringID schemacontract.OfferingID,
) (schemacontract.Offering, error) {
	if offeringID != "" {
		offering, ok := c.byID[offeringID]
		if ok && !offering.Enabled {
			if refusal := c.retiredOfferingRefusal(offering); refusal != nil {
				return schemacontract.Offering{}, refusal
			}
		}
		if !ok || !offering.Enabled {
			return schemacontract.Offering{}, &schemacontract.Error{
				Code:      schemacontract.ErrUnknownOffering,
				Operation: "select_offering",
				Detail:    fmt.Sprintf("未知或未启用的供给项 %q", offeringID),
			}
		}
		if offering.Capability != capability {
			return schemacontract.Offering{}, &schemacontract.Error{
				Code:      schemacontract.ErrInvalidInput,
				Operation: "select_offering",
				Detail:    fmt.Sprintf("供给项 %q 不支持能力 %q", offeringID, capability),
			}
		}
		return cloneOfferingModelProfiles(offering), nil
	}
	offerings := c.List(capability)
	if len(offerings) == 0 {
		return schemacontract.Offering{}, &schemacontract.Error{
			Code:      schemacontract.ErrNoOffering,
			Operation: "select_offering",
			Detail:    fmt.Sprintf("能力 %q 暂无可用供给项", capability),
		}
	}
	return offerings[0], nil
}

// creditsMeteredDeclaration records the first credits_metered value a
// Provider's Offering declared, so later Offerings can be checked against it.
type creditsMeteredDeclaration struct {
	metered    bool
	offeringID schemacontract.OfferingID
}

// validateDocumentOfferingLocalization checks offering translations against
// the offerings as written in the document.
func validateDocumentOfferingLocalization(doc document) error {
	if doc.Application.Localization == nil {
		return nil
	}
	encoded, err := json.Marshal(doc.Offerings)
	if err != nil {
		return fmt.Errorf("编码 offerings: %w", err)
	}
	var offerings []any
	if err := json.Unmarshal(encoded, &offerings); err != nil {
		return fmt.Errorf("解码 offerings: %w", err)
	}
	return ValidateOfferingLocalization(doc.Application.Localization, offerings)
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
