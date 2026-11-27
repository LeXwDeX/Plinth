package agentconfiguration

import "github.com/LeXwDeX/Plinth/internal/catalog"

// MarkPackagedForTest records skill as a packaged copy, the way a release's
// retired built-in metadata does, so a test can store one the Schema rejects.
func (s *Service) MarkPackagedForTest(skill catalog.SkillSchema) {
	fingerprint, err := catalog.RetiredSkillFingerprint(skill)
	if err != nil {
		panic(err)
	}
	s.packagedFingerprints[skill.ID] = fingerprint
}

// TolerantForTest runs the read-side tolerance of a stored configuration.
func (s *Service) TolerantForTest(c Configuration) error {
	_, _, err := s.tolerant("test", c)
	return err
}
