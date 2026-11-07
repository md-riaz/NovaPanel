package php

import (
	"fmt"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/validate"
)

// RenderPool renders the compatibility profile. The name is both the site
// account and pool name; maxChildren is a hard, positive concurrency cap.
func RenderPool(name string, maxChildren int) ([]byte, error) {
	return RenderPoolLimits(name, maxChildren, store.PHPLimits{})
}

// ValidateLimits validates overrides before persistence or rendering. Nil uses PHP's runtime default.
func ValidateLimits(limits store.PHPLimits) error {
	for _, value := range []*int{limits.MemoryMB, limits.UploadMB, limits.MaxExecSec} {
		if value != nil && (*value < 1 || *value > 1000000) {
			return rpcerr.Errf("invalid_argument", "invalid PHP limit")
		}
	}
	return nil
}

// RenderPoolLimits renders explicit overrides without changing defaults for omitted fields.
func RenderPoolLimits(name string, maxChildren int, limits store.PHPLimits) ([]byte, error) {
	if err := ValidateLimits(limits); err != nil {
		return nil, err
	}
	if err := (&validate.V{}).SiteName(name); err != nil {
		return nil, err
	}
	if maxChildren < 1 || maxChildren > 10000 {
		return nil, rpcerr.Errf("invalid_argument", "invalid FPM concurrency cap")
	}
	b := fmt.Sprintf(`[%s]
user = %s
group = %s
listen = /run/php/novapanel/%s.sock
listen.owner = www-data
listen.group = www-data
listen.mode = 0660
pm = ondemand
pm.max_children = %d
pm.process_idle_timeout = 10s
pm.max_requests = 500
php_admin_value[cgi.fix_pathinfo] = 0
php_admin_value[error_log] = /var/log/novapanel/%s/php-error.log
php_admin_flag[log_errors] = on
`, name, name, name, name, maxChildren, name)
	if limits.MemoryMB != nil {
		b += fmt.Sprintf("php_admin_value[memory_limit] = %dM\n", *limits.MemoryMB)
	}
	if limits.UploadMB != nil {
		b += fmt.Sprintf("php_admin_value[upload_max_filesize] = %dM\nphp_admin_value[post_max_size] = %dM\n", *limits.UploadMB, *limits.UploadMB)
	}
	if limits.MaxExecSec != nil {
		b += fmt.Sprintf("php_admin_value[max_execution_time] = %d\n", *limits.MaxExecSec)
	}
	return []byte(b), nil
}
