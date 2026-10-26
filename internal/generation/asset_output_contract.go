package generation

// AssetMatchesOutput checks a persisted asset against the source task's frozen
// output contract. It uses the same declaration specificity and media matching
// as materialization. Missing declarations fail closed; current Catalog entries
// must not change a historical task's contract.
func AssetMatchesOutput(outputs []AssetRequirement, role, variant, mediaType string) bool {
	requirement, declared := requirementForClaim(outputs, role, variant)
	return declared && mediaType != "" && mediaTypeAllowed(requirement.MediaTypes, mediaType)
}
