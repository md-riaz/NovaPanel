// Package platform centralizes Ubuntu 24.04 command paths and managed units.
package platform

import "novapanel/internal/exec"

const Systemctl = exec.Systemctl
const SystemdRun = exec.SystemdRun
const Busctl = exec.Busctl
const NginxRoot = "/etc/nginx"
const Nginx = "/usr/sbin/nginx"
const Chattr = "/usr/bin/chattr"

func ManageableUnit(unit string) bool {
	switch unit {
	case "nginx.service", "php8.3-fpm.service", "mariadb.service":
		return true
	}
	return false
}
