package catalog

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
func RetiredSkillFingerprint(skill SkillSchema) (string, error) {
	encoded, err := json.Marshal(skill)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
