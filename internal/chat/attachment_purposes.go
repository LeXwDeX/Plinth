package chat

import "errors"

var (
	ErrInvalidAttachmentPurpose           = errors.New("chat: invalid attachment purpose")
	ErrAttachmentPurposeAttachmentInvalid = errors.New("chat: attachment purpose references an invalid attachment")
)

type AttachmentPurpose struct {
	ImageSlot int
	Purpose   string
}

func validateAttachmentPurposes(purposes []AttachmentPurpose, imageCount int) error {
	seen := make(map[int]bool, len(purposes))
	input := false
	for _, purpose := range purposes {
		if purpose.ImageSlot < 1 || purpose.ImageSlot > imageCount || seen[purpose.ImageSlot] {
			return ErrInvalidAttachmentPurpose
		}
		seen[purpose.ImageSlot] = true
		switch purpose.Purpose {
		case "style", "layout":
		case "input":
			if input {
				return ErrInvalidAttachmentPurpose
			}
			input = true
		default:
			return ErrInvalidAttachmentPurpose
		}
	}
	return nil
}
