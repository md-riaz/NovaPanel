package tls

import "novapanel/internal/id"

// CheckRecovery validates retained coordinates and files without changing current.
func CheckRecovery(token Recovery) error {
	if !id.Valid(token.Version) || (token.Previous != "" && !id.Valid(token.Previous)) || token.Version == token.Previous {
		return invalid()
	}
	site, base, unlock, err := openPublication(token.Root, token.Primary, false)
	if err != nil {
		return err
	}
	defer base.Close()
	defer site.Close()
	defer unlock()
	current, err := currentVersion(site)
	if err != nil {
		return err
	}
	if current != token.Version && current != token.Previous {
		return invalid()
	}
	for _, v := range []string{token.Version, token.Previous} {
		if v != "" {
			if err = checkPairFiles(site, v); err != nil {
				return err
			}
		}
	}
	return nil
}
