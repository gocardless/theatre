package v1alpha1

import (
	"bytes"
	"encoding/json"
	"sort"
)

func (rc *ReleaseConfig) Equals(other *ReleaseConfig) bool {
	return bytes.Equal(rc.Serialise(), other.Serialise())
}

// The serialisation is used to determine if a release has changed.
// For release uniqueness we only take into consideration the target name,
// revision.name and revision.id.
func (rc *ReleaseConfig) Serialise() []byte {
	canonical := ReleaseConfig{
		TargetName: rc.TargetName,
		Revisions:  rc.Revisions,
	}

	for _, revision := range canonical.Revisions {
		var canonicalRevision Revision
		canonicalRevision.Name = revision.Name
		canonicalRevision.ID = revision.ID

		canonical.Revisions = append(canonical.Revisions, canonicalRevision)
	}

	sort.Slice(canonical.Revisions, func(i, j int) bool {
		return canonical.Revisions[i].Name < canonical.Revisions[j].Name
	})

	bytes, _ := json.Marshal(canonical)

	return bytes
}
