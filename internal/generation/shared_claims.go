package generation

import (
	"context"
	"io"
)

// Claims a Provider declared to have the same bytes (ArtifactClaim.SameBytesAs,
// #300) are downloaded once. The first of them to be stored serves the others:
// each of those is written again from the stored blob under its own slot (its
// own limit, media type, delivery profile and fidelity check), so a slot keeps
// its own asset record and nothing about it is judged on another slot's verdict.
// A claim whose group has nothing stored yet, because the first download failed
// or its store was refused, downloads for itself exactly as before.

// sharedClaimSource is what the first stored claim of a group leaves for the rest.
type sharedClaimSource struct {
	asset Asset
	// responseMediaType is the type the download reported. The claims of a group
	// come from one file, so the others resolve their own type from it as their
	// own download would have.
	responseMediaType string
}

// declaredIdenticalClaims maps the ID of every claim the Provider declared
// byte-identical to its group. A declaration joins two claims whichever of them
// carries it, and declarations chain, so the group does not depend on the order
// claims are stored in. Only claims that are read from a locator can belong to a
// group: a claim that carries its own bytes needs no download to save, and a
// declaration naming no such claim of the Observation (an unknown or empty ID,
// the claim itself, an ID two claims share) is ignored. It returns nil when no
// group exists.
func declaredIdenticalClaims(claims []ArtifactClaim) map[string]string {
	declared := false
	for _, claim := range claims {
		if claim.SameBytesAs != "" {
			declared = true
			break
		}
	}
	if !declared {
		return nil
	}
	seen := make(map[string]int, len(claims))
	for _, claim := range claims {
		seen[claim.ID]++
	}
	positions := make(map[string]int, len(claims))
	for position, claim := range claims {
		if claim.ID != "" && seen[claim.ID] == 1 && claim.Locator != "" && len(claim.Inline) == 0 && claim.StoredAsset == nil {
			positions[claim.ID] = position
		}
	}
	parent := make([]int, len(claims))
	for position := range parent {
		parent[position] = position
	}
	find := func(position int) int {
		for parent[position] != position {
			parent[position] = parent[parent[position]]
			position = parent[position]
		}
		return position
	}
	for _, claim := range claims {
		if claim.SameBytesAs == "" || claim.SameBytesAs == claim.ID {
			continue
		}
		own, declaring := positions[claim.ID]
		named, found := positions[claim.SameBytesAs]
		if !declaring || !found {
			continue
		}
		if left, right := find(own), find(named); left != right {
			parent[left] = right
		}
	}
	members := make(map[int]int, len(positions))
	for _, position := range positions {
		members[find(position)]++
	}
	groups := map[string]string{}
	for id, position := range positions {
		if root := find(position); members[root] > 1 {
			groups[id] = claims[root].ID
		}
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

// openSharedBytes reads the blob a claim of the same group stored. The store
// verifies its size and digest on open, so a damaged or missing file is an error
// here and the claim falls back to its own download.
func (m *Module) openSharedBytes(ctx context.Context, source sharedClaimSource) (io.ReadCloser, AssetMetadata, error) {
	body, err := m.assets.Open(ctx, source.asset)
	if err != nil {
		return nil, AssetMetadata{}, err
	}
	return body, AssetMetadata{MediaType: source.responseMediaType, Size: source.asset.Size}, nil
}
