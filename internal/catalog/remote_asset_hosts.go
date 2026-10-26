package catalog

import (
	"encoding/json"
	"errors"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// URL-producing wire mappings need a declared download host before any Task is
// accepted. The mapping vocabulary, not a Provider ID, determines this contract.
// Inline reports, base64 images and local outputs do not need remote hosts.
func validateRemoteAssetHosts(offering schemacontract.Offering) error {
	if len(offering.ProviderOptions) == 0 {
		return nil
	}
	var mappings struct {
		ResultAssets    []json.RawMessage `json:"result_assets"`
		ResponseAliases []json.RawMessage `json:"response_aliases"`
		Response        struct {
			URLField string `json:"url_field"`
		} `json:"response"`
	}
	if err := json.Unmarshal(offering.ProviderOptions, &mappings); err != nil {
		return err
	}
	if len(mappings.ResultAssets) == 0 && len(mappings.ResponseAliases) == 0 && mappings.Response.URLField == "" {
		return nil
	}
	for _, host := range offering.AssetHosts {
		// Match the existing downloader's normalization without changing the
		// frozen Schema values or expanding the declared host allowlist.
		if strings.Trim(strings.TrimSpace(host), ".") != "" {
			return nil
		}
	}
	return errors.New("URL 产物映射必须声明非空的 asset_hosts")
}
