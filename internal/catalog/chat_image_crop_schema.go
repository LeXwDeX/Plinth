package catalog

// ChatImageCropSchema is application.chat.image_crop (#331): the read-only
// crop_image_regions tool. It cuts normalized regions out of one owned
// picture at original resolution, stores every crop as a lossless PNG input
// asset of type list_crop and returns geometry only. No upstream call, Task,
// step or credits. Server side only; the browser never reads it.
type ChatImageCropSchema struct {
	// Tool names the control tool that implements it.
	Tool string `json:"tool"`
	// Texts are the Agent-facing refusal copy, keyed by the error code the
	// tool result carries. A refusal names one region, a limit or the source,
	// never another owner's asset.
	Texts ChatImageCropTexts `json:"texts"`
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
}
