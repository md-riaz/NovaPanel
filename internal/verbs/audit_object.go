package verbs

import (
	"context"

	"novapanel/internal/id"
	"novapanel/internal/rpc"
)

func (m siteMutationState) resolveDomainRemoval(ctx context.Context, args any) (string, error) {
	a := m.a
	v := args.(*childRemoveArgs)
	if v.SiteID != "" || v.RedirectID != "" || !id.Valid(v.DomainID) {
		return "", rpc.Errf("invalid_argument", "Invalid domain removal arguments")
	}
	d, err := a.Store.DomainByID(ctx, v.DomainID)
	if err != nil {
		return "", siteError(err)
	}
	return "site:" + d.SiteID, nil
}
func (m siteMutationState) resolveRedirectRemoval(ctx context.Context, args any) (string, error) {
	a := m.a
	v := args.(*childRemoveArgs)
	if v.SiteID != "" || v.DomainID != "" || !id.Valid(v.RedirectID) {
		return "", rpc.Errf("invalid_argument", "Invalid redirect removal arguments")
	}
	r, err := a.Store.RedirectByID(ctx, v.RedirectID)
	if err != nil {
		return "", siteError(err)
	}
	return "site:" + r.SiteID, nil
}
