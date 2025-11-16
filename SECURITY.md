# Security Documentation

## Overview
NovaPanel is designed with security as a core principle. This document outlines the security model and considerations.

## Non-Root Execution Model

### Panel User
The control panel runs as a dedicated non-root user (`novapanel` by default) with minimal system privileges.

```bash
# Create panel user
sudo useradd -r -m -d /opt/novapanel -s /bin/bash novapanel
```

### Sudo Configuration
Limited sudo access is granted only for essential system operations. The sudoers file should be configured as follows:

```
# /etc/sudoers.d/novapanel
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/useradd
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/usermod
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/userdel
novapanel ALL=(ALL) NOPASSWD: /bin/systemctl reload nginx
novapanel ALL=(ALL) NOPASSWD: /bin/systemctl reload php*-fpm
novapanel ALL=(ALL) NOPASSWD: /bin/mkdir
novapanel ALL=(ALL) NOPASSWD: /bin/chown
novapanel ALL=(ALL) NOPASSWD: /bin/chmod
novapanel ALL=(ALL) NOPASSWD: /usr/bin/crontab
novapanel ALL=(ALL) NOPASSWD: /bin/ln
novapanel ALL=(ALL) NOPASSWD: /bin/rm
```

## Shell Command Security

### Command Whitelisting
All shell commands are validated against a whitelist before execution. See `app/Infrastructure/Shell/Shell.php`:

**Allowed Commands:**
- nginx
- systemctl
- useradd, usermod, userdel
- mkdir, chown, chmod
- ln, rm, cp, mv
- crontab
- mysql, psql
- pure-pw
- pdns_control

### Argument Escaping
All command arguments are automatically escaped using `escapeshellarg()` to prevent shell injection attacks.

```php
// Example from Shell.php
$escapedArgs = array_map(fn($arg) => $this->escapeArg($arg), $args);
```

### Command Validation
Two-level validation:
1. Base command must be in the allowed list
2. Sudo commands must be in the sudo-approved list

## Directory Permissions

### Account Home Directories
```
/home/{username}/
├── public_html/    (755, owner: username)
├── logs/           (755, owner: username)
├── tmp/            (755, owner: username)
└── backups/        (755, owner: username)
```

### Configuration Files
- Nginx vhosts: `/etc/nginx/sites-available/` (644, owner: root)
- PHP-FPM pools: `/etc/php/{version}/fpm/pool.d/` (644, owner: root)
- Panel database: `/opt/novapanel/storage/panel.db` (640, owner: novapanel)

## Input Validation

### Domain Validation
```php
// Only alphanumeric, hyphens, dots, and valid TLD
preg_match('/^[a-z0-9]+([\-\.]{1}[a-z0-9]+)*\.[a-z]{2,}$/', $domain)
```

### Username Validation
```php
// Must start with letter, 3-32 characters
preg_match('/^[a-z][a-z0-9_-]{2,31}$/', $username)
```

## Database Security

### SQLite
- Database file permissions: 640 (owner: novapanel)
- Located in: `/opt/novapanel/storage/panel.db`
- Uses prepared statements to prevent SQL injection

### Password Hashing
All user passwords must be hashed using `password_hash()` with `PASSWORD_DEFAULT`:

```php
$hashedPassword = password_hash($password, PASSWORD_DEFAULT);
```

## Web Server Security

### Nginx Configuration
- Denies access to `.ht*` files
- PHP execution only for `.php` files
- Document root isolation per account
- FastCGI parameters properly configured

### PHP-FPM Isolation
- Each site runs in its own PHP-FPM pool
- Pool runs as the site's system user
- Isolated temp and session directories
- Resource limits can be set per pool

## Additional Security Features

### CSRF Protection
The panel includes Cross-Site Request Forgery (CSRF) protection for all forms:

```php
use App\Support\CSRF;

// Generate token in forms
echo CSRF::field();

// Verify token on submission
if (!CSRF::verify($_POST['_csrf_token'])) {
    throw new Exception('Invalid CSRF token');
}
```

### Session Security
Sessions are secured with:
- HTTP-only cookies
- Secure flag (HTTPS)
- SameSite=Strict
- Automatic regeneration every 5 minutes
- Session fingerprinting (User-Agent + IP)
- 1-hour timeout

### Rate Limiting
Brute force protection with rate limiting:
- Maximum 5 attempts per 15 minutes
- IP-based tracking
- Automatic lockout on exceeded attempts

```php
use App\Support\RateLimiter;

$key = 'login:' . $_SERVER['REMOTE_ADDR'];
if (RateLimiter::tooManyAttempts($key)) {
    $seconds = RateLimiter::availableIn($key);
    throw new Exception("Too many attempts. Try again in $seconds seconds.");
}
```

### Audit Logging
All shell command executions are logged to `/opt/novapanel/storage/logs/shell.log`:
- Timestamp
- User executing command
- Full command with arguments
- Exit code for failed commands

Log format:
```
[2025-11-16 16:00:00] USER=novapanel COMMAND=sudo useradd example
[2025-11-16 16:00:05] FAILED COMMAND=sudo nginx -t EXIT_CODE=1
```

## Best Practices

1. **Regular Updates**: Keep system packages updated
2. **Firewall**: Use UFW or iptables to restrict access (port 7080 is auto-configured)
3. **SSL/TLS**: Enable HTTPS for the panel and customer sites
4. **Backups**: Regular automated backups of panel database
5. **Monitoring**: Monitor logs for suspicious activity (`/opt/novapanel/storage/logs/`)
6. **Fail2ban**: Built-in rate limiting provides basic brute force protection
7. **Audit Logs**: Review shell.log regularly for unauthorized access attempts

## Security Checklist

- [x] Panel runs as non-root user
- [x] Sudo whitelist is minimal and specific
- [x] All shell commands use `escapeshellarg()`
- [x] Command whitelist enforced
- [x] Directory permissions properly set
- [x] Input validation on all user data
- [x] Prepared statements for database queries
- [x] Password hashing with strong algorithm
- [x] CSRF protection implemented
- [x] Session security with regeneration
- [x] Rate limiting for brute force prevention
- [x] Audit logging for shell commands
- [x] Firewall configured (port 7080)
- [ ] HTTPS enabled for panel (recommended for production)
- [ ] Regular security updates scheduled (admin responsibility)

## Reporting Security Issues

If you discover a security vulnerability, please email security@novapanel.dev with:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

**Do not** create public GitHub issues for security vulnerabilities.
