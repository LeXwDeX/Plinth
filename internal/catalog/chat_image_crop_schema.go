package catalog

// ChatImageCropSchema is application.chat.image_crop (#331): the read-only
// crop_image_regions tool. It cuts normalized regions out of one owned
// picture at original resolution, stores every crop as a lossless PNG input
// asset of type list_crop and returns geometry only. No upstream call, Task,
// step or credits. Server side only; the browser never reads it.
type ChatImageCropSchema struct {
	// Tool names the control tool that implements it.
	Tool string `json:"tool"`
	// Limits bound one crop of this tool, apart from the asset-list decompose
	// crop (application.asset_list.decompose.crop), which keeps its own
	// smaller limits (#642).
	Limits ChatImageCropLimits `json:"limits"`
	// Texts are the Agent-facing refusal copy, keyed by the error code the
	// tool result carries. A refusal names one region, a limit or the source,
	// never another owner's asset.
	Texts ChatImageCropTexts `json:"texts"`
}

// ChatImageCropLimits bounds one crop of the Agent tool (#642). A crop is a
// deterministic pixel copy with no model input budget, so the region cap
// lets a 4096x4096 board be cut whole. Memory is bounded by the source
// decode limits (chat.image_mask.limits), which the region cap may not
// exceed; the PNG cap may not exceed runtime.input_assets.upload_max_bytes,
// the storage cap of an ordinary input asset. Leaving both zero (an older
// Catalog) keeps the decompose crop limits for the tool.
type ChatImageCropLimits struct {
	// MaximumRegionPixels is the largest area one crop may cover.
	MaximumRegionPixels int64 `json:"maximum_region_pixels"`
	// MaximumPNGBytes bounds the encoded PNG of one crop.
	MaximumPNGBytes int64 `json:"maximum_png_bytes"`
}

// ChatImageCropTexts words the tool's refusals. {detail} carries the measured
// reason (or the refused region), {limit} the source's crop limit.
type ChatImageCropTexts struct {
	// InvalidArguments refuses an unreadable argument object, an empty
	// picture, or a handle that is not a picture of this session. {detail}
	InvalidArguments string `json:"invalid_arguments"`
	// InvalidRegion refuses a region outside the picture or without area.
	// {detail}
	InvalidRegion string `json:"invalid_region"`
	// ImageTooSmall refuses a region whose shortest side is under the
	// Schema minimum. {detail}
	ImageTooSmall string `json:"image_too_small"`
	// ImageTooLarge refuses a source picture, region or encoded crop over a
	// size limit. {detail}
	ImageTooLarge string `json:"image_too_large"`
	// CropLimitReached refuses a new rectangle once the source keeps the
	// maximum number of crops. {limit}
	CropLimitReached string `json:"crop_limit_reached"`
	// ImageNotFound refuses a source that does not exist or belongs to
	// another owner. {detail}
	ImageNotFound string `json:"image_not_found"`
	// ImageUnavailable refuses a source that cannot be read or decoded right
	// now. {detail}
	ImageUnavailable string `json:"image_unavailable"`
	// PresentedNote rides in a successful result: the crops are session
	// pictures of the reply, each with its handle, and only what the person
	// can see counts as delivered (#552). {count}
	PresentedNote string `json:"presented_note"`
	// NotPresentable refuses a call before any crop is stored when the
	// session cannot show crops to the person as handled pictures (#552).
	// {detail}
	NotPresentable string `json:"not_presentable"`
	// NotPresented replaces the result when the stored crops did not all
	// become handled pictures of the reply (#552). {detail}
	NotPresented string `json:"not_presented"`
}
