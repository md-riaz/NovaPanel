// Package php renders PHP 8.3 FPM pools without filesystem or process access.
package php

import (
	"fmt"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
)

// Desired contains trusted resolved site IDs, not account names or raw directives.
// UID and GID must come from the provisioned site's account. Render does not resolve accounts.
// Paths, socket permissions and process-manager capacity are fixed by the Phase 4 contract.
type Desired struct {
	Name, Type, Version string
	UID, GID            uint32
	Limits              *Limits // nil uses creation defaults; non-nil must be a complete valid tuple.
}

// Render returns pool bytes only. Publishing, account custody and no-follow path checks
// belong to the future privileged apply layer, not this pure renderer.
func Render(d Desired) ([]byte, error) {
	if err := (&validate.V{}).SiteName(d.Name); err != nil {
		return nil, err
	}
	if (d.Type != "php" && d.Type != "wordpress") || d.Version != "8.3" {
		return nil, rpcerr.Errf("invalid_argument", "PHP 8.3 requires a PHP or WordPress site")
	}
	if d.UID == 0 || d.GID == 0 || d.UID == ^uint32(0) || d.GID == ^uint32(0) {
		return nil, rpcerr.Errf("invalid_argument", "Invalid site UID or GID")
	}
	limits, err := MergeLimits(d.Limits, LimitsPatch{})
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`[%s]
user = %d
group = %d
listen = /run/php/novapanel/%s.sock
listen.owner = www-data
listen.group = www-data
listen.mode = 0660
pm = ondemand
pm.max_children = 2
pm.process_idle_timeout = 10s
pm.max_requests = 500
php_admin_value[cgi.fix_pathinfo] = 0
php_admin_value[error_log] = /var/log/novapanel/%s/php-error.log
php_admin_flag[log_errors] = on
php_admin_value[memory_limit] = %dM
php_admin_value[post_max_size] = %dM
php_admin_value[upload_max_filesize] = %dM
php_admin_value[max_execution_time] = %d
`, d.Name, d.UID, d.GID, d.Name, d.Name, limits.MemoryMB, limits.UploadMB, limits.UploadMB, limits.MaxExecSec)), nil
}
