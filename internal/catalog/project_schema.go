package catalog

// ProjectSchema is application.project (#240): the owner-scoped project
// contract. A project holds a reusable style guide, character/prop references
// and production conventions; sessions inherit them across conversations.
// The server owns the limits, Agent copy, audit actions and error messages.
type ProjectSchema struct {
	Limits      ProjectLimitsSchema      `json:"limits"`
	StyleGuide  ProjectStyleGuideSchema  `json:"style_guide"`
	Assets      ProjectAssetsSchema      `json:"assets"`
	Conventions ProjectConventionsSchema `json:"conventions"`
	// Agent declares the read-only list_project_assets tool and the
	// server-side context templates (#240). Optional so a Catalog that only
	// ships the project data model is valid; a non-nil value is fully
	// validated.
	Agent    *ProjectAgentSchema   `json:"agent,omitempty"`
	Audit    ProjectAuditSchema    `json:"audit"`
	Messages ProjectMessagesSchema `json:"messages"`
}

// ProjectLimitsSchema bounds the owner's projects and one project's assets.
type ProjectLimitsSchema struct {
	MaximumProjectsPerOwner           int `json:"maximum_projects_per_owner"`
	MaximumNameCharacters             int `json:"maximum_name_characters"`
	MaximumAssetsPerProject           int `json:"maximum_assets_per_project"`
	MaximumAssetNameCharacters        int `json:"maximum_asset_name_characters"`
	MaximumAssetDescriptionCharacters int `json:"maximum_asset_description_characters"`
	MaximumAssetImages                int `json:"maximum_asset_images"`
	MaximumAssetModels                int `json:"maximum_asset_models"`
	MaximumListItems                  int `json:"maximum_list_items"`
}

// ProjectStyleGuideSchema is the project's reusable style guide. Palette
// entries are colours matching PaletteColorPattern (for example "#1f2937").
type ProjectStyleGuideSchema struct {
	MaximumTextCharacters int    `json:"maximum_text_characters"`
	MaximumReferences     int    `json:"maximum_references"`
	MaximumPaletteColors  int    `json:"maximum_palette_colors"`
	PaletteColorPattern   string `json:"palette_color_pattern"`
}

// ProjectAssetsSchema declares the kinds of project asset the browser may
// create. LabelKey names a content.messages entry; kinds stay server
// validated and the public projection publishes only id and label_key.
type ProjectAssetsSchema struct {
	Kinds []ProjectAssetKindSchema `json:"kinds"`
}

type ProjectAssetKindSchema struct {
	ID       string `json:"id"`
	LabelKey string `json:"label_key"`
}

// ProjectConventionsSchema declares the production defaults a project may
// carry. Fields reuses the production-brief field declarations (and must be a
// same-name, same-type subset of chat.model3d_usage.brief.fields); Texts adds
// free-form naming/coordinate conventions. Neither table is duplicated.
type ProjectConventionsSchema struct {
	Fields []ChatProductionBriefField    `json:"fields"`
	Texts  []ProjectConventionTextSchema `json:"texts"`
}

type ProjectConventionTextSchema struct {
	Name              string `json:"name"`
	LabelKey          string `json:"label_key"`
	MaximumCharacters int    `json:"maximum_characters"`
}

// ProjectAgentSchema is the server-side Agent contract of a project (#240):
// the read-only tool name, its context block and the @projectN handle prefix.
type ProjectAgentSchema struct {
	ToolName             string `json:"tool_name"`
	ContextTemplate      string `json:"context_template"`
	AssetLineTemplate    string `json:"asset_line_template"`
	CitationTemplate     string `json:"citation_template"`
	UnknownAssetTemplate string `json:"unknown_asset_template"`
	ListLimitTemplate    string `json:"list_limit_template"`
	StylePromptTemplate  string `json:"style_prompt_template"`
	HandlePrefix         string `json:"handle_prefix"`
	ListLimit            int    `json:"list_limit"`
}

// ProjectAuditSchema declares the product-audit actions a project operation
// records. Both resource types describe the object the action wrote.
type ProjectAuditSchema struct {
	CreateAction        string `json:"create_action"`
	UpdateAction        string `json:"update_action"`
	DeleteAction        string `json:"delete_action"`
	PromoteAction       string `json:"promote_action"`
	ProjectResourceType string `json:"project_resource_type"`
	AssetResourceType   string `json:"asset_resource_type"`
}

// ProjectMessagesSchema is the copy the project HTTP boundary returns. Limit
// templates name the maximum they enforce; the three style-guide templates
// explain the inherited/overridden/disabled source to the person.
type ProjectMessagesSchema struct {
	NameRequired           string `json:"name_required"`
	NameTooLong            string `json:"name_too_long"`
	ProjectLimit           string `json:"project_limit"`
	AssetLimit             string `json:"asset_limit"`
	StyleTextTooLong       string `json:"style_text_too_long"`
	PaletteColorInvalid    string `json:"palette_color_invalid"`
	ReferenceLimit         string `json:"reference_limit"`
	ReferenceInvalid       string `json:"reference_invalid"`
	UnknownProject         string `json:"unknown_project"`
	AssetNotFound          string `json:"asset_not_found"`
	PromotionSourceInvalid string `json:"promotion_source_invalid"`
	StyleGuideInherited    string `json:"style_guide_inherited"`
	StyleGuideOverridden   string `json:"style_guide_overridden"`
	StyleGuideOff          string `json:"style_guide_off"`
}

// PublicProjectSchema is the browser-visible part of application.project.
// Limits, asset kinds, style-guide bounds and the convention names are
// enough to build the project switcher and panels; Agent templates, audit
// actions and error messages stay server-side.
type PublicProjectSchema struct {
	Limits      ProjectLimitsSchema            `json:"limits"`
	Assets      ProjectAssetsSchema            `json:"assets"`
	StyleGuide  ProjectStyleGuideSchema        `json:"style_guide"`
	Conventions PublicProjectConventionsSchema `json:"conventions"`
}

// PublicProjectConventionsSchema publishes the allowed convention field names
// and free-form text names without their server-side validation rules.
type PublicProjectConventionsSchema struct {
	Fields []string `json:"fields"`
	Texts  []string `json:"texts"`
}
