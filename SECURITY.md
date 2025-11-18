# Security Documentation

## Overview
NovaPanel is designed with security as a core principle. This document outlines the security model and considerations.

## Authentication & Authorization

### Session-Based Authentication
NovaPanel uses secure session-based authentication for all panel access:

- **Login Required**: All routes (except `/login`) require authentication
- **Session Management**: Secure HTTP-only cookies with SameSite protection
- **Session Timeout**: 1-hour inactivity timeout
- **Session Regeneration**: Automatic session ID regeneration every 5 minutes
- **Session Fingerprinting**: User-Agent and IP validation to prevent session hijacking

### Authentication Middleware
The `AuthMiddleware` protects all routes by:
1. Checking for valid user session
2. Redirecting unauthenticated requests to `/login`
3. Validating session integrity and timeout

### Password Security
- Passwords are hashed using `password_hash()` with `PASSWORD_DEFAULT` (bcrypt)
- Server-side password confirmation validation
- Minimum 8 character password requirement
- Passwords are never stored in plain text or logged

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
# Single VPS Model: Only ONE Linux user (novapanel) exists
# No user creation/modification/deletion commands allowed (useradd/usermod/userdel)
novapanel ALL=(ALL) NOPASSWD: /bin/systemctl reload nginx
novapanel ALL=(ALL) NOPASSWD: /bin/systemctl reload php*-fpm
novapanel ALL=(ALL) NOPASSWD: /bin/systemctl reload bind9
novapanel ALL=(ALL) NOPASSWD: /bin/systemctl reload named
novapanel ALL=(ALL) NOPASSWD: /bin/mkdir
novapanel ALL=(ALL) NOPASSWD: /bin/chown
novapanel ALL=(ALL) NOPASSWD: /bin/chmod
novapanel ALL=(ALL) NOPASSWD: /usr/bin/crontab
novapanel ALL=(ALL) NOPASSWD: /bin/ln
novapanel ALL=(ALL) NOPASSWD: /bin/rm
novapanel ALL=(ALL) NOPASSWD: /bin/cp
novapanel ALL=(ALL) NOPASSWD: /bin/mv
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/nginx -t
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/named-checkconf
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/named-checkzone
novapanel ALL=(ALL) NOPASSWD: /usr/bin/pure-pw
novapanel ALL=(ALL) NOPASSWD: /bin/bash
```

**Note:** This configuration is automatically created by the `install.sh` script. Only manual configuration is needed if you're performing a manual installation or if you need to update an existing installation.

**Important:** NovaPanel follows a **Single VPS Model** where only ONE Linux system user (`novapanel`) exists. Panel users are database records only, not Linux system accounts. Therefore, user management commands (useradd, usermod, userdel) are intentionally excluded from the sudoers configuration and are never used by the panel.

## Shell Command Security

### Command Whitelisting
All shell commands are validated against a whitelist before execution. See `app/Infrastructure/Shell/Shell.php`:

**Allowed Commands:**
- nginx
- systemctl
- mkdir, chown, chmod
- ln, rm, cp, mv
- crontab
- mysql, psql
- pure-pw (for FTP virtual users)
- named-checkzone, named-checkconf (for BIND9 zone validation)
- bash

**Explicitly Disallowed Commands:**
- useradd, usermod, userdel (not needed in single-user model)

### Argument Escaping
All command arguments are automatically escaped using `escapeshellarg()` to prevent shell injection attacks.

```php
// Example from Shell.php
$escapedArgs = array_map(fn($arg) => $this->escapeArg($arg), $args);
```

### Command Validation
Multi-level validation prevents command injection:
1. **Command purity check**: Commands containing whitespace or shell metacharacters (`;|&$`<>(){}[]\\`) are rejected
2. **Whitelist validation**: Only exact command names in the allowed list can be executed
3. **Sudo restriction**: Additional validation for commands requiring sudo privileges
4. **Argument separation**: Commands and arguments are kept strictly separate to prevent injection

```php
// Commands must be pure - no spaces or metacharacters allowed
$this->execute('nginx', ['-t']); // ✓ Valid
$this->execute('nginx -t', []); // ✗ Rejected - contains whitespace
$this->execute('nginx; rm -rf /', []); // ✗ Rejected - contains metacharacters
```

### Privileged File Writing
Configuration files under `/etc` require root privileges to write. The Shell wrapper provides a secure `writeFile()` method:

```php
// Secure file writing using sudo
$this->shell->writeFile('/etc/nginx/sites-available/example.com.conf', $content);
// 1. Writes content to temporary file
// 2. Uses sudo cp to move to destination
// 3. Sets proper permissions (644)
// 4. Cleans up temporary file
```

This approach ensures:
- Panel runs as unprivileged user
- No direct file writes to privileged directories
- All operations are logged in audit trail
- Proper file permissions are maintained

## Web Terminal Security

NovaPanel includes a web-based terminal feature powered by **ttyd**. This feature is designed with security in mind:

### Terminal Isolation
- Each panel user gets an isolated terminal session
- Sessions run on unique ports (7100 + user_id) to prevent cross-user access
- All terminal sessions run as the `novapanel` system user (not root)
- Terminal processes are managed by the TerminalAdapter with strict lifecycle controls

### Authentication & Authorization
- Terminal sessions require credential-based authentication
- Random tokens are generated for each session (32 hex characters)
- Tokens are stored securely and validated on each connection
- Basic auth format: `novapanel:<random_token>`
- Sessions are tied to panel user IDs (requires authentication to be implemented)

### Process Management
- Terminal processes (ttyd) are tracked via PID files
- Automatic cleanup of stale sessions
- Process verification before considering a session active
- Graceful termination with fallback to force kill if needed

### Network Security
- Terminal WebSocket connections are proxied through Nginx
- Connections only accepted from localhost by default
- Can be configured to use SSL/TLS in production
- Rate limiting can be applied at the Nginx level

### Session Security
- Sessions automatically timeout after inactivity
- PID and session information stored in protected directories:
  - `/opt/novapanel/storage/terminal/pids/` (750, owner: novapanel)
  - `/opt/novapanel/storage/terminal/logs/` (750, owner: novapanel)
- Session tokens are regenerated on each new session
- Old sessions are properly terminated before creating new ones

### Audit Trail
- All terminal sessions are logged
- Start/stop events are recorded
- Failed session attempts are logged
- Terminal output can be logged for compliance (optional)

### Best Practices
1. **Enable authentication**: Integrate with the Session class before production use
2. **Use HTTPS**: Enable SSL/TLS for the panel and terminal connections
3. **Monitor sessions**: Regularly check active sessions with `getActiveSessions()`
4. **Set timeouts**: Configure appropriate session timeout values
5. **Review logs**: Check terminal logs for suspicious activity
6. **Limit access**: Use RBAC to control who can access the terminal feature

### ttyd Security Configuration
The terminal adapter configures ttyd with secure defaults:
```bash
ttyd -p <port> -c novapanel:<token> -t fontSize=14 -W bash -l
```
- `-p`: Specific port for isolation
- `-c`: Credential authentication required
- `-W`: Writable terminal (required for command execution)
- `bash -l`: Login shell with proper environment

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
**Critical Security Feature - Prevents Path Traversal Attacks**

Usernames are strictly validated to prevent path traversal vulnerabilities when creating directories:

```php
// Must start with letter, 3-32 characters, only letters, numbers, hyphens, and underscores
// This prevents attacks like: ../../tmp or ../../../etc/passwd
preg_match('/^[a-z][a-z0-9_-]{2,31}$/i', $username)
```

**Why This Matters:**
- Site directories are created at `/opt/novapanel/sites/{$username}/`
- Without validation, a username like `../../tmp` could create directories outside the intended path
- This validation ensures usernames cannot contain path separators or special characters
- Applied in both user creation and update operations

### Password Validation
**Critical Security Feature - Server-Side Password Confirmation**

Password changes require server-side confirmation matching to prevent mismatched passwords:

```php
// Both create and update operations validate password confirmation
if ($password !== $passwordConfirm) {
    throw new \Exception('Password and password confirmation do not match');
}
```

**Why This Matters:**
- Client-side validation can be bypassed by attackers
- Without server-side validation, an attacker could submit mismatched passwords
- This would create users with passwords different from what the operator expects
- Applied in both user creation and update operations (when password is being changed)

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
- Secure flag (automatically enabled when using HTTPS)
- SameSite=Strict
- Automatic regeneration every 5 minutes
- Session fingerprinting (User-Agent + IP)
- 1-hour timeout

**Note:** The `session.cookie_secure` flag is automatically set based on the connection type:
- When accessed over HTTPS, secure cookies are enabled
- When accessed over HTTP (development/initial setup), secure cookies are disabled
- For production deployments, always enable HTTPS to ensure secure cookies are used

### Rate Limiting
Brute force protection with rate limiting on authentication:
- Maximum 5 login attempts per 15 minutes per IP address
- IP-based tracking using file-based cache
- Automatic lockout on exceeded attempts
- Clear rate limit on successful login
- Displays remaining time until rate limit resets

```php
use App\Support\RateLimiter;

$key = 'login:' . $_SERVER['REMOTE_ADDR'];
if (RateLimiter::tooManyAttempts($key)) {
    $seconds = RateLimiter::availableIn($key);
    throw new Exception("Too many attempts. Try again in $seconds seconds.");
}
```

### Comprehensive Audit Logging
NovaPanel maintains comprehensive audit logs for security monitoring and compliance:

#### Shell Command Logging
All shell command executions are logged to `/opt/novapanel/storage/logs/shell.log`:
- Timestamp
- User executing command
- Full command with arguments
- Exit code for failed commands

Log format:
```
[2025-11-17 00:35:00] USER=novapanel COMMAND=sudo useradd example
[2025-11-17 00:35:05] FAILED COMMAND=sudo nginx -t EXIT_CODE=1
```

#### Admin Action Logging
All administrative actions are logged to `/opt/novapanel/storage/logs/audit.log`:
- Authentication events (login/logout)
- Resource creation (users, sites, databases, FTP, cron, DNS)
- Resource updates (user details, roles)
- Resource deletion (all resources)
- User who performed the action
- Timestamp and IP address
- Detailed context (parameters, IDs, etc.)

Log format:
```
[2025-11-17 00:35:13] ACTION=user.login MESSAGE=User 'admin' logged in CONTEXT={"user_id":1,"ip":"::1"}
[2025-11-17 00:35:20] ACTION=site.created MESSAGE=site 'example.com' was created CONTEXT={"user_id":2,"php_version":"8.2","ssl_enabled":true}
[2025-11-17 00:35:25] ACTION=database.deleted MESSAGE=database 'mydb' was deleted CONTEXT={"database_id":5,"db_type":"mysql"}
```

#### Using the AuditLogger
```php
use App\Support\AuditLogger;

// Log custom events
AuditLogger::log('custom.action', 'Description of action', ['key' => 'value']);

// Log resource creation
AuditLogger::logCreated('resource_type', 'resource_name', ['details']);

// Log resource updates
AuditLogger::logUpdated('resource_type', 'resource_name', ['details']);

// Log resource deletion
AuditLogger::logDeleted('resource_type', 'resource_name', ['details']);
```

## DNS Security (BIND9)

### Complete Database Isolation
NovaPanel uses BIND9 with zone files instead of a database-backed DNS solution. This provides several security advantages:

- **No Database Access**: DNS data is stored in zone files under `/etc/bind/zones/`, completely isolated from customer database access
- **No SQL Injection**: Zone files eliminate SQL injection attack vectors entirely
- **Simpler Security Model**: File-based permissions are easier to audit and secure than database-level access controls
- **Industry Standard**: BIND9 is the most widely deployed DNS server, battle-tested for decades

### Zone File Security
- Zone files owned by `bind:bind` user
- File permissions set to 644 (read-only for panel)
- Panel uses sudo to manage zone files with proper validation
- All zone files validated with `named-checkzone` before deployment
- Configuration validated with `named-checkconf` before reload

### DNS Operations Security
- All zone modifications increment serial number automatically
- Zone file syntax validated before applying changes
- Failed validations are logged and changes are rolled back
- BIND9 service reloaded (not restarted) to minimize downtime
- Separate zone files per domain prevent cross-contamination

### BIND9 Configuration
- Recursion disabled (authoritative DNS only)
- Query logging can be enabled for audit trails
- Zone transfers configurable per-zone
- DNSSEC support available for enhanced security

## Configuration Security

### Credential Management
- Sensitive credentials stored in `.env.php` (excluded from version control)
- File permissions set to 600 (readable only by panel user)
- Example configuration file provided (`.env.php.example`)
- Never commit `.env.php` to version control
- Credentials loaded via environment variables (`putenv()`)

### Configuration Files
```bash
# Set secure permissions on configuration (www-data group can read for PHP-FPM)
chmod 640 /opt/novapanel/.env.php
chown novapanel:www-data /opt/novapanel/.env.php

# Configuration file structure
/opt/novapanel/
├── .env.php              # Actual credentials (git-ignored)
├── .env.php.example      # Template (version controlled)
└── config/               # Configuration logic
    ├── app.php           # Application settings
    └── database.php      # Database configuration
```

## Best Practices

1. **Authentication**: Ensure all users have strong passwords (minimum 8 characters)
2. **Regular Updates**: Keep system packages updated
3. **Firewall**: Use UFW or iptables to restrict access (port 7080 is auto-configured)
4. **SSL/TLS**: Enable HTTPS for the panel and customer sites
5. **Backups**: Regular automated backups of panel database and audit logs
6. **Log Monitoring**: Monitor logs for suspicious activity
   - `/opt/novapanel/storage/logs/audit.log` - Admin actions
   - `/opt/novapanel/storage/logs/shell.log` - Shell commands
7. **Rate Limiting**: Built-in protection against brute force attacks (5 attempts per 15 minutes)
8. **Session Security**: Sessions automatically expire after 1 hour of inactivity
9. **Audit Review**: Regularly review audit logs for:
   - Failed login attempts
   - Unauthorized resource access
   - Suspicious resource creation/deletion patterns
   - After-hours administrative actions

## Security Checklist

### Authentication & Access Control
- [x] Session-based authentication implemented
- [x] All routes protected with authentication middleware (except /login)
- [x] Login/logout functionality working
- [x] Password hashing with strong algorithm (bcrypt)
- [x] Session timeout after 1 hour of inactivity
- [x] Session regeneration every 5 minutes
- [x] Session fingerprinting (User-Agent + IP)
- [x] CSRF protection on all forms
- [x] Rate limiting for brute force prevention (5 attempts per 15 minutes)

### System Security
- [x] Panel runs as non-root user
- [x] Sudo whitelist is minimal and specific
- [x] All shell commands use `escapeshellarg()`
- [x] Command whitelist enforced
- [x] Directory permissions properly set
- [x] Input validation on all user data
- [x] Prepared statements for database queries

### Configuration & Credentials
- [x] Configuration file (.env.php) excluded from version control
- [x] Example configuration file provided (.env.php.example)
- [x] Secure file permissions on configuration (600)
- [x] Credentials loaded via environment variables
- [x] Database credentials moved to config
- [x] BIND9 configuration paths in config (no database credentials needed)

### Logging & Monitoring
- [x] Comprehensive audit logging for admin actions
- [x] Audit logging for resource creation/deletion
- [x] Authentication event logging (login/logout)
- [x] Shell command logging
- [x] Failed command logging with exit codes
- [x] Firewall configured (port 7080)

### Web Terminal
- [x] Web terminal with credential authentication
- [x] Terminal session isolation per user
- [x] Terminal process management and cleanup
- [x] Terminal authentication integrated with Session

### Production Readiness
- [ ] HTTPS enabled for panel (recommended for production)
- [ ] SSL certificates installed
- [ ] Regular security updates scheduled (admin responsibility)
- [ ] Backup strategy implemented
- [ ] Log rotation configured

## Reporting Security Issues

If you discover a security vulnerability, please email security@novapanel.dev with:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

**Do not** create public GitHub issues for security vulnerabilities.
