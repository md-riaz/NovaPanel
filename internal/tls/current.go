package tls

// CurrentRecovery retains the installed pointer without copying or changing its pair.
// Restore refuses an unexpected writer rather than overwriting its publication.
func CurrentRecovery(root, primary string) (Recovery, error) {
	site, base, unlock, err := openPublication(root, primary, false)
	if err != nil {
		return Recovery{}, err
	}
	defer base.Close()
	defer site.Close()
	defer unlock()
	version, err := currentVersion(site)
	if err != nil {
		return Recovery{}, err
	}
	if err = checkPairFiles(site, version); err != nil {
		return Recovery{}, err
	}
	return Recovery{Root: root, Primary: primary, Version: version, Previous: version}, nil
}
