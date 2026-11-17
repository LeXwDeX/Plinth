package imagereview

// The check assembly shared by the platform inspect_image tool and the external
// MCP inspector (#332 7b-2): resolve the Schema checklist, judge a delivery
// picture against its 2D purpose, measure the final bytes and write the Agent's
// next step. The callers keep their own picture resolution, byte reads, region
// crop and round/visual handling.

import (
	"errors"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

// errUnknownChecklist is the package refusal for a name the Schema does not
// declare. Callers word their own result from it, so the text stays theirs.
var errUnknownChecklist = errors.New("unknown checklist")

// Purpose is the 2D purpose a delivery inspection judged the picture against.
type Purpose struct {
	ID, Label, Source, Note string
}

// Inspection is one check of final bytes, ready for a caller to shape into its
// own result. MediaType and Size describe the inspected bytes, not a declared
// picture.
type Inspection struct {
	Report        imageinspect.Report
	ChecklistText string
	Purpose       *Purpose
	Checks        []imageinspect.Check
	MediaType     string
	Size          int64
}

// inspectBytes runs the checklist over the final bytes, judging a delivery
// picture against its brief's purpose.
func (service *Service) inspectBytes(data []byte, checklistName string, brief chat.ProductionBrief) (Inspection, error) {
	return service.inspectBytesFor(data, checklistName, brief, true)
}

// inspectBytesFor is inspectBytes with the purpose checks switchable. A region
// crop is a part of the delivered picture (its short edge, seams and edge are
// not the picture's), so the platform tool judges no purpose for it.
func (service *Service) inspectBytesFor(data []byte, checklistName string, brief chat.ProductionBrief, judgePurpose bool) (Inspection, error) {
	name := checklistName
	if name == "" {
		name = "delivery"
	}
	var judgement *purposeJudgement
	if judgePurpose && name == "delivery" {
		judgement = service.judgement(brief)
	}
	return service.inspectBytesWith(data, checklistName, judgement, imageinspect.Limits{
		MaxBytes:  service.review.Limits.MaxBytes,
		MaxPixels: service.review.Limits.MaxPixels,
	})
}

// inspectBytesWith is the assembly for callers that resolved the purpose
// themselves (the session's brief, or an explicit argument). A nil judgement
// runs no purpose checks; requirements are then zero.
func (service *Service) inspectBytesWith(data []byte, checklistName string, judgement *purposeJudgement,
	limits imageinspect.Limits,
) (Inspection, error) {
	if checklistName == "" {
		checklistName = "delivery"
	}
	checklist, checklistText, ok := service.checklist(checklistName)
	if !ok {
		return Inspection{}, errUnknownChecklist
	}
	var requirements imageinspect.Requirements
	if judgement != nil {
		requirements = judgement.requirements
	}
	report, err := imageinspect.InspectFor(data, limits, requirements)
	if err != nil {
		return Inspection{}, err
	}
	checked := Inspection{
		Report: report, ChecklistText: checklistText,
		Checks: report.Review(checklist), MediaType: mediaTypeOf(report.Format), Size: int64(len(data)),
	}
	if judgement != nil {
		purposeChecks := report.ReviewRequirements(judgement.requirements)
		service.addHints(purposeChecks, judgement)
		checked.Checks = append(checked.Checks, purposeChecks...)
		checked.Purpose = &Purpose{
			ID: judgement.profile.ID, Label: judgement.profile.Label,
			Source: judgement.source, Note: service.note(judgement),
		}
	}
	return checked, nil
}

// mediaTypeOf names the container the bytes actually are, never a declared
// type. An unsupported format decodes to an error before this is asked.
func mediaTypeOf(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}
