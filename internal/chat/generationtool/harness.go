package generationtool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func turnHarnessBinding(command chat.ToolCommand) harness.Binding {
	if command.Options.Harness == nil {
		return harness.Binding{}
	}
	return command.Options.Harness.Selection.Binding
}
func activeHarness(command chat.ToolCommand) bool {
	return command.Options.Harness != nil && command.Options.Harness.Selection.OperationID != ""
}

func (executor *Executor) checkCurrentHarness(ctx context.Context, command chat.ToolCommand) error {
	if command.Options.Harness == nil {
		return nil
	}
	snapshot := command.Options.Harness
	if !snapshot.Selection.Binding.Valid() || snapshot.Selection.Digest == "" || snapshot.Selection.SessionID != command.SessionID || (snapshot.Selection.OwnerID != "" && snapshot.Selection.OwnerID != command.OwnerID) {
		return ErrOperationConflict
	}
	selected := snapshot.Selection
	selected.OwnerID = command.OwnerID
	normalized, err := harness.NormalizeSelection(selected)
	if err != nil || normalized.Digest != selected.Digest {
		return ErrOperationConflict
	}
	if executor.harness == nil {
		return errors.New("generation tool: Harness selection store unavailable")
	}
	current, err := executor.harness.GetHarnessSelection(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return err
	}
	if current.Binding != snapshot.Selection.Binding {
		return harness.ErrStale
	}
	return nil
}
func (executor *Executor) harnessRefusal(command chat.ToolCommand, err error) (chat.ToolResult, error) {
	if errors.Is(err, resolve.ErrSourceClarification) {
		message := executor.chat.Harness.SourceClarificationMessage
		if message == "" {
			message = executor.chat.Harness.MissingInputMessage
		}
		return executor.reject(command, message)
	}
	if errors.Is(err, harness.ErrStale) {
		return executor.reject(command, executor.chat.Harness.ChangedMessage)
	}
	if errors.Is(err, ErrOperationConflict) {
		return executor.reject(command, executor.chat.Harness.RejectionMessage)
	}
	return chat.ToolResult{NoSubmission: true}, err
}

// executeHarnessOperation only consumes the frozen goal and source. Legacy
// model, session overrides, Skill settings and reference history never enter it.
func (executor *Executor) executeHarnessOperation(ctx context.Context, command chat.ToolCommand, observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	snapshot := command.Options.Harness
	if err := executor.checkCurrentHarness(ctx, command); err != nil {
		return executor.harnessRefusal(command, err)
	}
	bound, bindErr := bindHarnessSource(command)
	if bindErr != nil {
		return executor.harnessRefusal(command, bindErr)
	}
	command = bound
	snapshot = command.Options.Harness
	if !executor.UsesExecutionGrants() {
		return chat.ToolResult{NoSubmission: true}, errors.New("generation tool: Harness requires execution grants")
	}
	if snapshot.Status != resolve.StatusReady {
		if snapshot.Status == resolve.StatusNeedsInput && snapshot.CallableWithMissing {
			if message := executor.missingParametersMessage(command, *snapshot); message != "" {
				return executor.reject(command, message)
			}
		}
		return executor.reject(command, executor.chat.Harness.MissingInputMessage)
	}
	if snapshot.ToolName != command.Call.Name || snapshot.ToolName != command.Tool.Name || snapshot.Capability == "" || !slices.Contains(command.Tool.Execution.Capabilities, snapshot.Capability) || snapshot.OfferingRevision != snapshot.Selection.OfferingRevision {
		return executor.harnessRefusal(command, ErrOperationConflict)
	}
	// Duplicate keys and trailing JSON cannot silently change the authorized value.
	arguments, err := decodeHarnessObject(command.Call.Arguments, command.OwnerID, command.SessionID)
	if err != nil {
		return executor.harnessRefusal(command, ErrOperationConflict)
	}
	for key := range arguments {
		if key != "offering_id" && key != "parameters" && key != "parameter_reasons" &&
			!(key == "source" && resolve.AgentSource(snapshot.Selection)) {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
	}
	if raw, exists := arguments["offering_id"]; exists {
		var id string
		if json.Unmarshal(raw, &id) != nil || id != snapshot.Selection.OfferingID {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
	}
	parameters := map[string]json.RawMessage{}
	if raw, exists := arguments["parameters"]; exists {
		parameters, err = decodeHarnessObject(raw, command.OwnerID, command.SessionID)
		if err != nil {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
	}
	var offering generation.Offering
	found := false
	for _, candidate := range executor.gateway.Offerings(snapshot.Capability) {
		if string(candidate.ID) == snapshot.Selection.OfferingID && candidate.Revision == snapshot.Selection.OfferingRevision && candidate.Capability == snapshot.Capability {
			offering = candidate
			found = true
			break
		}
	}
	if !found || !offering.Enabled || !executor.gateway.OfferingAvailable(offering.ID) {
		return executor.reject(command, executor.chat.Harness.UnavailableMessage)
	}
	declared := map[string]generation.Parameter{}
	for _, p := range offering.Parameters {
		declared[p.Name] = p
	}
	input := map[string]json.RawMessage{}
	for name, raw := range snapshot.Input {
		input[name] = append(json.RawMessage(nil), raw...)
	}
	for name, fixed := range snapshot.Selection.FixedParameters {
		if !sameHarnessJSON(input[name], fixed, command) {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
	}
	for name, raw := range input {
		p, exists := declared[name]
		if !exists {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
		if _, fixed := snapshot.Selection.FixedParameters[name]; fixed {
			if !offering.UserParameterAllowed(p) {
				return executor.harnessRefusal(command, ErrOperationConflict)
			}
			continue
		}
		if _, source := schema.SourceParameterRole(p); !source || !json.Valid(raw) {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
	}
	for name, value := range parameters {
		p, exists := declared[name]
		if exists && resolve.ConversationParameter(p) && slices.Contains(snapshot.ConversationParameters, name) {
			// A picture of this chat, named by its handle (#187 D). It is
			// resolved to an owned input asset like any reference image.
			if _, fixed := snapshot.Selection.FixedParameters[name]; fixed {
				return executor.harnessRefusal(command, ErrOperationConflict)
			}
			bound, refusal, err := executor.conversationInput(ctx, command, p, value)
			if err != nil {
				return chat.ToolResult{NoSubmission: true}, err
			}
			if refusal != nil {
				return *refusal, nil
			}
			input[name] = bound
			continue
		}
		if !exists || !harnessEditable(offering, p) {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
		if fixed, exists := snapshot.Selection.FixedParameters[name]; exists {
			if !sameHarnessJSON(fixed, value, command) {
				return executor.harnessRefusal(command, ErrOperationConflict)
			}
			continue
		}
		if !slices.Contains(snapshot.Selection.FillableParameters, name) {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
		normalized, err := catalog.ValidateHarnessParameter(p, value)
		if err != nil {
			return executor.harnessRefusal(command, ErrOperationConflict)
		}
		input[name] = normalized
	}
	if err := executor.verifyHarnessSource(ctx, command, offering, input); err != nil {
		return executor.harnessRefusal(command, err)
	}
	if missing := executor.missingConversationInputs(command, offering, snapshot.ConversationParameters, input); len(missing) > 0 {
		return executor.reject(command, strings.ReplaceAll(
			executor.localized(command, executor.chat.Harness.MissingConversationInputMessage),
			"{parameters}", strings.Join(missing, executor.harnessParameterSeparator(command))))
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	return executor.prepareAgentOperation(ctx, command, snapshot.Capability, offering, encoded, "", command.IdempotencyKey(), referenceUse{}, referenceSelection{}, nil, nil, observe)
}

// conversationInput resolves the image handles an Agent gave for a
// conversation input to owned input assets: one for an asset parameter, the
// declared count for an asset-array. Only handles of this session's pictures
// are accepted; an asset ID or a URL is not a handle and is refused.
func (executor *Executor) conversationInput(ctx context.Context, command chat.ToolCommand, parameter generation.Parameter, raw json.RawMessage) (json.RawMessage, *chat.ToolResult, error) {
	refuse := func(message string) (json.RawMessage, *chat.ToolResult, error) {
		result, err := executor.reject(command, message)
		return nil, &result, err
	}
	invalid := strings.ReplaceAll(executor.localized(command, executor.chat.Harness.MissingConversationInputMessage),
		"{parameters}", executor.harnessParameterLabel(command, parameter))
	var tokens []string
	if parameter.Type == "asset-array" {
		if json.Unmarshal(raw, &tokens) != nil || len(tokens) == 0 || (parameter.MaxItems > 0 && len(tokens) > parameter.MaxItems) || len(tokens) < parameter.MinItems {
			return refuse(invalid)
		}
	} else {
		var token string
		if json.Unmarshal(raw, &token) != nil {
			return refuse(invalid)
		}
		tokens = []string{token}
	}
	seen := map[string]bool{}
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" || seen[token] {
			return refuse(invalid)
		}
		seen[token] = true
	}
	urls, err := executor.resolveImageHandles(command, tokens)
	if err != nil {
		return refuse(err.Error())
	}
	if len(urls) != len(tokens) {
		return refuse(invalid)
	}
	assets, err := executor.references.Resolve(ctx, generation.OwnerID(command.OwnerID), urls)
	if result, refused := executor.inputRefusal(command, err); refused {
		return nil, &result, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(assets) != len(urls) {
		return refuse(invalid)
	}
	var value any = assets
	if parameter.Type != "asset-array" {
		value = assets[0]
	}
	encoded, err := json.Marshal(value)
	return encoded, nil, err
}

// missingConversationInputs names the required conversation inputs a call
// left out, by their Schema labels in the turn's locale.
func (executor *Executor) missingConversationInputs(command chat.ToolCommand, offering generation.Offering, names []string, input map[string]json.RawMessage) []string {
	values := map[string]any{}
	for name, raw := range input {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			values[name] = value
		}
	}
	missing := []string{}
	for _, parameter := range offering.Parameters {
		if !slices.Contains(names, parameter.Name) || !parameter.Required || generation.ParameterOmitted(parameter, values) {
			continue
		}
		if raw, present := input[parameter.Name]; present && len(raw) > 0 && string(raw) != "null" {
			continue
		}
		missing = append(missing, executor.harnessParameterLabel(command, parameter))
	}
	return missing
}

// missingParametersMessage names the parameters a needs_input turn still
// lacks; empty when the frozen Offering no longer declares them.
func (executor *Executor) missingParametersMessage(command chat.ToolCommand, snapshot resolve.Snapshot) string {
	template := executor.localized(command, executor.chat.Harness.MissingParametersMessage)
	if template == "" || len(snapshot.MissingParameters) == 0 {
		return ""
	}
	labels := []string{}
	for _, offering := range executor.gateway.Offerings(snapshot.Capability) {
		if string(offering.ID) != snapshot.Selection.OfferingID || offering.Revision != snapshot.Selection.OfferingRevision {
			continue
		}
		for _, name := range snapshot.MissingParameters {
			for _, parameter := range offering.Parameters {
				if parameter.Name == name {
					labels = append(labels, executor.harnessParameterLabel(command, parameter))
				}
			}
		}
	}
	if len(labels) == 0 {
		return ""
	}
	return strings.ReplaceAll(template, "{parameters}", strings.Join(labels, executor.harnessParameterSeparator(command)))
}

// harnessParameterLabel names a parameter in the turn's locale. Only a Schema
// label is translated; the name fallback is an identifier.
func (executor *Executor) harnessParameterLabel(command chat.ToolCommand, parameter generation.Parameter) string {
	if strings.TrimSpace(parameter.Label) != "" {
		return executor.localized(command, parameter.Label)
	}
	return parameter.Name
}

// harnessParameterSeparator joins parameter labels in the turn's locale,
// keeping the default Schema separator for a nil localizer.
func (executor *Executor) harnessParameterSeparator(command chat.ToolCommand) string {
	if separator := executor.texts.Message(command.Options.Locale, "ui.harness.separator"); separator != "" {
		return separator
	}
	return "、"
}

func harnessEditable(o generation.Offering, p generation.Parameter) bool {
	return o.AgentParameterAllowed(p)
}

func decodeHarnessObject(raw []byte, owner, session string) (map[string]json.RawMessage, error) {
	normalized, err := harness.NormalizeSelection(harness.Selection{OwnerID: owner, SessionID: session, FixedParameters: map[string]json.RawMessage{"object": raw}})
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(normalized.FixedParameters["object"], &object); err != nil || object == nil {
		return nil, ErrOperationConflict
	}
	return object, nil
}
func sameHarnessJSON(a, b json.RawMessage, command chat.ToolCommand) bool {
	left, err := harness.NormalizeSelection(harness.Selection{OwnerID: command.OwnerID, SessionID: command.SessionID, FixedParameters: map[string]json.RawMessage{"value": a}})
	if err != nil {
		return false
	}
	right, err := harness.NormalizeSelection(harness.Selection{OwnerID: command.OwnerID, SessionID: command.SessionID, FixedParameters: map[string]json.RawMessage{"value": b}})
	return err == nil && left.Digest == right.Digest
}
func (executor *Executor) verifyHarnessSource(ctx context.Context, command chat.ToolCommand, offering generation.Offering, input map[string]json.RawMessage) error {
	snapshot := command.Options.Harness
	if len(snapshot.BoundSources()) != 1 || len(snapshot.SourceFingerprints) != 1 {
		return ErrOperationConflict
	}
	source, proof := snapshot.BoundSources()[0], snapshot.SourceFingerprints[0]
	if source.Kind == harness.SourceInputAsset {
		if executor.harnessResolver == nil {
			return ErrOperationConflict
		}
		if err := executor.harnessResolver.VerifyUploadedSource(ctx, command.OwnerID, *snapshot, offering, input); err != nil {
			if errors.Is(err, harness.ErrStale) || errors.Is(err, harness.ErrInvalid) ||
				generation.IsCode(err, generation.ErrNotFound) || generation.IsCode(err, generation.ErrInvalidModelInput) ||
				generation.IsCode(err, generation.ErrUnsupportedModelInput) || generation.IsCode(err, generation.ErrModelInputLimit) ||
				generation.IsCode(err, generation.ErrConflict) {
				return ErrOperationConflict
			}
			return err
		}
		return nil
	}
	if proof.Kind != "" && proof.Kind != harness.SourceTaskAsset {
		return ErrOperationConflict
	}
	if source.TaskID == "" || proof.TaskID != generation.TaskID(source.TaskID) || (source.AssetID != "" && proof.AssetID != generation.AssetID(source.AssetID)) {
		return ErrOperationConflict
	}
	task, err := executor.gateway.Get(ctx, generation.OwnerID(command.OwnerID), proof.TaskID)
	if err != nil {
		if generation.IsCode(err, generation.ErrNotFound) {
			return ErrOperationConflict
		}
		return err
	}
	if task.ID != proof.TaskID || task.OwnerID != generation.OwnerID(command.OwnerID) || task.SessionID != command.SessionID || task.State != generation.TaskSucceeded {
		return ErrOperationConflict
	}
	assetFound := false
	for _, asset := range task.Assets {
		if asset.ID == proof.AssetID && asset.Role == "model" && asset.StorageKey != "" && (asset.TaskID == "" || asset.TaskID == task.ID) && asset.SHA256 == proof.SHA256 && asset.Size == proof.Size && asset.Variant == proof.Variant {
			assetFound = true
			break
		}
	}
	if !assetFound {
		return ErrOperationConflict
	}
	var prefer []string
	if command.Tool.Processing != nil {
		prefer = command.Tool.Processing.SourceVariantPreference
	}
	route := offering
	route.Parameters = nil
	for _, parameter := range offering.Parameters {
		if _, isSource := schema.SourceParameterRole(parameter); !isSource || parameter.Name == source.Parameter {
			route.Parameters = append(route.Parameters, parameter)
		}
	}
	binding, err := generation.BindModelSource(route, task, generation.AssetID(source.AssetID), prefer)
	if err != nil || binding.Parameter != source.Parameter || proof.Parameter != source.Parameter {
		return ErrOperationConflict
	}
	expected, _ := json.Marshal(binding.Value)
	if !sameHarnessJSON(input[binding.Parameter], expected, command) {
		return ErrOperationConflict
	}
	return nil
}

// proposeHarness is a read-only plan-tool branch. It cannot reach Processing.Create.
func (executor *Executor) proposeHarness(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	if command.Call.Name != executor.chat.Harness.PrerequisiteTool || command.Tool.Name != command.Call.Name {
		return executor.harnessRefusal(command, ErrOperationConflict)
	}
	if err := executor.checkCurrentHarness(ctx, command); err != nil {
		return executor.harnessRefusal(command, err)
	}
	if executor.harnessResolver == nil {
		return executor.reject(command, executor.chat.Harness.RejectionMessage)
	}
	arguments, err := decodeHarnessObject(command.Call.Arguments, command.OwnerID, command.SessionID)
	if err != nil || len(arguments) != 1 {
		return executor.harnessRefusal(command, ErrOperationConflict)
	}
	var path []string
	if json.Unmarshal(arguments["path"], &path) != nil || len(path) == 0 {
		return executor.harnessRefusal(command, ErrOperationConflict)
	}
	snapshot := *command.Options.Harness
	snapshot.Selection.OwnerID = command.OwnerID
	proposal, err := executor.harnessResolver.Propose(ctx, snapshot, path)
	if errors.Is(err, harness.ErrInvalid) {
		return executor.reject(command, executor.chat.Harness.MissingInputMessage)
	}
	if err != nil {
		return executor.harnessRefusal(command, err)
	}
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	return chat.ToolResult{NoSubmission: true,
		AgentContent: executor.localized(command, executor.chat.Harness.ProposalMessage) + "\n" + string(encoded)}, nil
}

func fixedHarnessInputMatches(command chat.ToolCommand, input json.RawMessage) bool {
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return false
	}
	for name, expected := range command.Options.Harness.Selection.FixedParameters {
		if !sameHarnessJSON(values[name], expected, command) {
			return false
		}
	}
	for _, source := range command.Options.Harness.BoundSources() {
		if !sameHarnessJSON(values[source.Parameter], command.Options.Harness.Input[source.Parameter], command) {
			return false
		}
	}
	return true
}

// ForcedWhen may change an otherwise editable field. Refuse such a change to
// a human-fixed value before PrepareInput; the frozen kernel result is checked
// again for all normalization effects before an operation or grant is created.
func forcedHarnessInputMatches(command chat.ToolCommand, offering generation.Offering, input json.RawMessage) bool {
	values := map[string]any{}
	for _, parameter := range offering.Parameters {
		if len(parameter.Default) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(parameter.Default))
			decoder.UseNumber()
			var value any
			if decoder.Decode(&value) != nil {
				return false
			}
			values[parameter.Name] = value
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var actual map[string]any
	if decoder.Decode(&actual) != nil {
		return false
	}
	for name, value := range actual {
		values[name] = value
	}
	generation.ApplyForcedValues(offering.Parameters, values)
	encoded, err := json.Marshal(values)
	return err == nil && fixedHarnessInputMatches(command, encoded)
}
