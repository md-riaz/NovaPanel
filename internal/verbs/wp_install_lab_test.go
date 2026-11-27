//go:build wordpress_offline_lab && linux

package verbs

import "novapanel/internal/rpc"

func init() {
	rpc.Register(rpc.Verb{Name: "wp.install", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &wpInstallArgs{} }, NewJobArgs: func() any { return &wpInstallJobArgs{} }, JobObjectKey: func(args any) string { return "site:" + args.(*wpInstallJobArgs).SiteID }, ObjectKey: func(args any) string { return "site:" + args.(*wpInstallArgs).SiteID }, Run: wpInstall, Exec: wpInstallExec})
}
