// Package platform centralizes Ubuntu 24.04 command paths and managed units.
package platform

const Systemctl = "/usr/bin/systemctl"
const SystemdRun = "/usr/bin/systemd-run"
const Busctl = "/usr/bin/busctl"
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
