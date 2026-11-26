package catalog

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// The old packaged images remain available for saved, user-edited Skills.
// This file contains only identities, fingerprints and local image paths.
//
//go:embed retired_skills.json
var retiredSkillMetadataJSON []byte

type RetiredSkill struct {
	ID            string `json:"id"`
	Fingerprint   string `json:"fingerprint"`
	ThumbnailPath string `json:"thumbnail_path"`
}

var retiredSkillMetadata struct {
	sync.Once
	skills []RetiredSkill
	err    error
}

func RetiredBuiltInSkills() ([]RetiredSkill, error) {
	retiredSkillMetadata.Do(func() {
		var doc struct {
			Skills []RetiredSkill `json:"skills"`
		}
		if retiredSkillMetadata.err = json.Unmarshal(retiredSkillMetadataJSON, &doc); retiredSkillMetadata.err != nil {
			return
		}
		if len(doc.Skills) == 0 {
			retiredSkillMetadata.err = fmt.Errorf("retired Skill metadata is empty")
			return
		}
		seen := map[string]bool{}
		for _, skill := range doc.Skills {
			if skill.ID == "" || skill.ThumbnailPath == "" || len(skill.Fingerprint) != 64 || seen[skill.ID] {
				retiredSkillMetadata.err = fmt.Errorf("invalid retired Skill metadata for %q", skill.ID)
				return
			}
			seen[skill.ID] = true
		}
		retiredSkillMetadata.skills = doc.Skills
	})
	if retiredSkillMetadata.err != nil {
		return nil, retiredSkillMetadata.err
	}
	return append([]RetiredSkill(nil), retiredSkillMetadata.skills...), nil
}

// RetiredSkillFingerprint compares the complete saved content. The metadata
// was generated from old defaults after replacing remote image provenance
// with the packaged thumbnail path, exactly as the old service saved it.
//
// The digest is taken over fingerprintedSkill, the fields SkillSchema had when
// the fingerprints were made, so a field added to SkillSchema later does not
// change any fingerprint while it is left at its zero value (#665). A Skill
// that sets such a field is not the packaged content: its digest then covers
// the complete encoding and matches no fingerprint.
func RetiredSkillFingerprint(skill SkillSchema) (string, error) {
	frozen, err := json.Marshal(fingerprintedSkill{
		ID: skill.ID, Name: skill.Name, NameZH: skill.NameZH, Category: skill.Category,
		RecommendedSize: skill.RecommendedSize, ThumbnailPath: skill.ThumbnailPath, RawImageURL: skill.RawImageURL,
		Prompt: skill.Prompt, ImageToImageTemplate: skill.ImageToImageTemplate, Tags: skill.Tags,
		Slug: skill.Slug, Description: skill.Description, Apply: skill.Apply, Tool: skill.Tool,
		RequiresImage: skill.RequiresImage, DefaultMessage: skill.DefaultMessage, OneShot: skill.OneShot,
	})
	if err != nil {
		return "", err
	}
	full, err := json.Marshal(skill)
	if err != nil {
		return "", err
	}
	return skillFingerprint(frozen, full)
}

// fingerprintedSkill is SkillSchema as the fingerprints were made: the same
// fields, JSON names and order. It never gains a field.
type fingerprintedSkill struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	NameZH               string   `json:"name_zh,omitempty"`
	Category             string   `json:"category"`
	RecommendedSize      string   `json:"recommended_size"`
	ThumbnailPath        string   `json:"thumbnail_path"`
	RawImageURL          string   `json:"raw_image_url"`
	Prompt               string   `json:"prompt"`
	ImageToImageTemplate string   `json:"image_to_image_template,omitempty"`
	Tags                 []string `json:"tags,omitempty"`
	Slug                 string   `json:"slug,omitempty"`
	Description          string   `json:"description,omitempty"`
	Apply                string   `json:"apply,omitempty"`
	Tool                 string   `json:"tool,omitempty"`
	RequiresImage        bool     `json:"requires_image,omitempty"`
	DefaultMessage       string   `json:"default_message,omitempty"`
	OneShot              bool     `json:"one_shot,omitempty"`
}

// skillFingerprint digests frozen, the encoding of the fingerprinted fields,
// unless full, the complete encoding, has a key outside them whose value is not
// a JSON zero value (null, "", false, 0, [], {}): then it digests full.
func skillFingerprint(frozen, full []byte) (string, error) {
	var known, all map[string]json.RawMessage
	if err := json.Unmarshal(frozen, &known); err != nil {
		return "", err
	}
	if err := json.Unmarshal(full, &all); err != nil {
		return "", err
	}
	content := frozen
	for key, value := range all {
		if _, found := known[key]; !found && !zeroJSON(value) {
			content = full
			break
		}
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func zeroJSON(value json.RawMessage) bool {
	switch strings.TrimSpace(string(value)) {
	case "null", `""`, "false", "0", "[]", "{}":
		return true
	}
	return false
}
