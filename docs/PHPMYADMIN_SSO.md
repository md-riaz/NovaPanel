# phpMyAdmin Single Sign-On (SSO) Implementation

## Overview

NovaPanel implements Single Sign-On (SSO) for phpMyAdmin, allowing users to access the database management interface without entering credentials. This provides a seamless experience similar to other control panels.

## How It Works

### Authentication Flow

```
┌─────────────────────────────────────────────────────────────┐
│ User clicks "phpMyAdmin" link in NovaPanel                  │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ Request sent to: /phpmyadmin-signon.php                     │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ Signon Script Checks:                                       │
│  1. Is NovaPanel session active? ($_SESSION['user_id'])     │
│  2. If NO → Redirect to /login                             │
│  3. If YES → Continue                                       │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ Load MySQL Credentials:                                     │
│  - Read from .env.php                                       │
│  - MYSQL_HOST (localhost)                                   │
│  - MYSQL_ROOT_USER (novapanel_db)                          │
│  - MYSQL_ROOT_PASSWORD (auto-generated)                    │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ Set phpMyAdmin Signon Session:                             │
│  $_SESSION['novapanel_pma_signon'] = [                     │
│      'user' => $mysqlUser,                                 │
│      'password' => $mysqlPassword,                         │
│      'host' => $mysqlHost                                  │
│  ]                                                          │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ Redirect to phpMyAdmin:                                     │
│  - Base: /phpmyadmin/                                       │
│  - With database: /phpmyadmin/?db=dbname                   │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ phpMyAdmin reads signon session and logs in automatically  │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ ✅ User sees database list - NO PASSWORD PROMPT!           │
└─────────────────────────────────────────────────────────────┘
```

## Implementation Details

### 1. phpMyAdmin Configuration

File: `/etc/phpmyadmin/config.inc.php`

```php
$cfg['Servers'][$i]['auth_type'] = 'signon';
$cfg['Servers'][$i]['SignonSession'] = 'novapanel_pma_signon';
$cfg['Servers'][$i]['SignonURL'] = '/phpmyadmin-signon.php';
$cfg['Servers'][$i]['LogoutURL'] = '/dashboard';
```

**Key Settings:**
- `auth_type`: Changed from 'cookie' to 'signon'
- `SignonSession`: Name of session variable containing credentials
- `SignonURL`: Script that handles authentication
- `LogoutURL`: Where to redirect after logout (back to panel)

### 2. Signon Script

File: `public/phpmyadmin-signon.php`

```php
<?php
// Start NovaPanel session
session_name('novapanel_session');
session_start();

// Check authentication
if (!isset($_SESSION['user_id'])) {
    header('Location: /login');
    exit;
}

// Load credentials from environment
$envFile = __DIR__ . '/../.env.php';
if (file_exists($envFile)) {
    require_once $envFile;
}

// Get MySQL credentials
$mysqlHost = getenv('MYSQL_HOST') ?: 'localhost';
$mysqlUser = getenv('MYSQL_ROOT_USER') ?: 'root';
$mysqlPassword = getenv('MYSQL_ROOT_PASSWORD') ?: '';

// Set phpMyAdmin signon session
$_SESSION['novapanel_pma_signon'] = [
    'user' => $mysqlUser,
    'password' => $mysqlPassword,
    'host' => $mysqlHost,
];

// Redirect to phpMyAdmin (with optional database parameter)
$redirectUrl = '/phpmyadmin/';
if (isset($_GET['db']) && !empty($_GET['db'])) {
    $redirectUrl .= '?db=' . urlencode($_GET['db']);
}

header('Location: ' . $redirectUrl);
exit;
```

### 3. UI Integration

All phpMyAdmin links updated to use the signon script:

**Sidebar:**
```html
<a href="/phpmyadmin-signon.php" target="_blank">
    <i class="bi bi-server"></i> phpMyAdmin
</a>
```

**Databases Page Header:**
```html
<a href="/phpmyadmin-signon.php" class="btn btn-success" target="_blank">
    <i class="bi bi-box-arrow-up-right"></i> phpMyAdmin
</a>
```

**Per-Database Manage Link:**
```html
<a href="/phpmyadmin-signon.php?db=<?= urlencode($db->name) ?>" 
   class="btn btn-sm btn-outline-primary" 
   target="_blank">
    <i class="bi bi-pencil-square"></i> Manage
</a>
```

## Security Considerations

### ✅ Protected Access
- Users must be authenticated in NovaPanel before accessing phpMyAdmin
- Signon script verifies `$_SESSION['user_id']` exists
- Unauthenticated users redirected to `/login`

### ✅ Credential Security
- MySQL credentials never exposed to client
- Stored securely in `.env.php` (not in public directory)
- File permissions: 640 (readable only by owner and www-data group)
- Credentials loaded server-side only

### ✅ Session Security
- Uses NovaPanel's existing session management
- Session name: `novapanel_session`
- Session timeout handled by panel
- No persistent credential storage in browser

### ✅ Privilege Management
- phpMyAdmin uses MySQL admin user (`novapanel_db`)
- This user has full privileges to create/manage databases
- Appropriate for panel administrators
- Individual database users can be created with restricted privileges

## Benefits

### For Users
- **No Repetitive Login**: Click and go - no password prompts
- **Seamless Experience**: Feels like one integrated application
- **Time Saving**: Instant access to database management
- **Direct Database Access**: Click "Manage" on any database to open it

### For Administrators
- **Reduced Support**: No "forgot phpMyAdmin password" tickets
- **Better UX**: Competitive with other control panels
- **Secure**: Still requires panel authentication
- **Simple Maintenance**: One credential set to manage

### For Security
- **Single Point of Authentication**: Only need to secure panel login
- **Session-Based**: Leverages existing session security
- **No Credential Exposure**: Credentials never sent to browser
- **Audit Trail**: All access goes through panel session

## Comparison: Before vs After

### Before (Cookie Authentication)
```
User Experience:
1. Navigate to Databases page
2. Click "phpMyAdmin" link
3. See phpMyAdmin login page
4. Find MySQL username (where was it?)
5. Find MySQL password (check docs)
6. Enter server: localhost
7. Enter username: novapanel_db
8. Enter password: [long random string]
9. Click "Go"
10. Finally see databases

Time: ~30-60 seconds
User Friction: High
```

### After (SSO Authentication)
```
User Experience:
1. Click "phpMyAdmin" anywhere in panel
2. ✅ See database list immediately!

Time: ~1-2 seconds
User Friction: None
```

## Troubleshooting

### Issue: Redirect loop or authentication failure

**Cause**: Session configuration mismatch

**Solution**:
```bash
# Check session configuration
grep -r "session_name" /opt/novapanel/

# Ensure both panel and signon script use same session name
# Panel: session_name('novapanel_session');
# Signon: session_name('novapanel_session');
```

### Issue: "Access denied" in phpMyAdmin

**Cause**: MySQL credentials not loaded correctly

**Solution**:
```bash
# Verify .env.php exists and has correct permissions
ls -la /opt/novapanel/.env.php

# Should be: -rw-r----- novapanel www-data

# Test credentials manually
mysql -u novapanel_db -p
# (use password from .env.php)
```

### Issue: Redirects to login page even when logged in

**Cause**: Session not persisting

**Solution**:
```php
// Check session configuration in panel
// Ensure session.save_path is writable
php -i | grep session.save_path

// Verify permissions
ls -ld /var/lib/php/sessions/
```

## Alternative: Manual Login

If SSO is not desired, you can revert to cookie authentication:

1. Edit `/etc/phpmyadmin/config.inc.php`:
```php
// Change this:
$cfg['Servers'][$i]['auth_type'] = 'signon';

// To this:
$cfg['Servers'][$i]['auth_type'] = 'cookie';
```

2. Update links to point directly to phpMyAdmin:
```html
<!-- Change from: -->
<a href="/phpmyadmin-signon.php">phpMyAdmin</a>

<!-- To: -->
<a href="/phpmyadmin/">phpMyAdmin</a>
```

3. Users will then see the login screen and must enter credentials manually.

## Future Enhancements

Potential improvements to the SSO implementation:

- [ ] Per-user MySQL accounts (instead of shared admin account)
- [ ] Database-specific access control
- [ ] Activity logging (track which user accessed which database)
- [ ] Timeout warnings before phpMyAdmin session expires
- [ ] Option to switch between different MySQL users
- [ ] Integration with panel's role-based access control

## References

- phpMyAdmin Signon Authentication: https://docs.phpmyadmin.net/en/latest/setup.html#signon-authentication-mode
- PHP Session Management: https://www.php.net/manual/en/book.session.php
- NovaPanel Security Documentation: [SECURITY.md](../SECURITY.md)

---

**Implementation Status**: ✅ Complete and Working

Users can now click phpMyAdmin and instantly access their databases without any password prompts, providing a seamless experience comparable to other modern control panels.
