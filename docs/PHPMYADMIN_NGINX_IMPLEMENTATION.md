# phpMyAdmin with Nginx Implementation in NovaPanel

## Overview

NovaPanel implements phpMyAdmin using **Nginx only** - no Apache web server is required or used. This document explains how NovaPanel handles phpMyAdmin installation, configuration, and integration with Nginx, addressing common concerns about web server conflicts.

## Why Nginx Only?

Unlike some control panels that introduce Apache alongside Nginx (causing port conflicts and complexity), NovaPanel uses a **single web server architecture**:

- ✅ **Only Nginx** - No Apache installation
- ✅ **Single Port** - phpMyAdmin served on port 7080 (same as panel)
- ✅ **No Conflicts** - No web server port conflicts
- ✅ **Better Performance** - Nginx handles all HTTP traffic efficiently
- ✅ **Simpler Setup** - One web server to configure and maintain

## Installation Process

### Automatic Installation

When you run `install.sh`, phpMyAdmin is automatically installed and configured:

```bash
sudo bash install.sh
```

The installer performs these steps:

1. **Installs phpMyAdmin Package**
   ```bash
   apt-get install -y phpmyadmin
   ```
   - During Debian/Ubuntu package installation, you'll be asked which web server to configure
   - **Select "None"** - we configure Nginx manually
   - Do NOT select Apache2 or Lighttpd

2. **Creates phpMyAdmin Configuration**
   - Location: `/etc/phpmyadmin/config.inc.php`
   - Generates secure blowfish secret
   - Configures signon authentication for SSO
   - Sets proper security options

3. **Configures Nginx**
   - Adds location block to `/etc/nginx/sites-available/novapanel.conf`
   - Maps `/phpmyadmin` URL to phpMyAdmin files
   - Configures PHP-FPM processing
   - Handles static assets (CSS, JS, images)

4. **Sets Up Single Sign-On**
   - Implements automatic login from NovaPanel
   - No password prompts required
   - Secure session-based authentication

## Nginx Configuration Details

### Location Block in Nginx

The installer adds this configuration to Nginx:

```nginx
# phpMyAdmin location
location /phpmyadmin {
    alias /usr/share/phpmyadmin;
    index index.php;

    # Process PHP files
    location ~ ^/phpmyadmin/(.+\.php)$ {
        alias /usr/share/phpmyadmin/$1;
        fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $request_filename;
        include fastcgi_params;
    }

    # Serve static assets
    location ~* ^/phpmyadmin/(.+\.(jpg|jpeg|gif|css|png|js|ico|html|xml|txt))$ {
        alias /usr/share/phpmyadmin/$1;
    }
}
```

### Key Configuration Points

1. **Alias Directive**: Maps `/phpmyadmin` URL to `/usr/share/phpmyadmin/` directory
2. **PHP Processing**: Uses PHP-FPM (not Apache mod_php)
3. **FastCGI Pass**: Connects to PHP 8.2 FPM socket
4. **Static Assets**: Directly served by Nginx for performance
5. **No .htaccess**: Nginx doesn't use .htaccess files (unlike Apache)

## phpMyAdmin Configuration

### Authentication Mode: Signon

NovaPanel uses **signon authentication** for automatic login:

```php
// /etc/phpmyadmin/config.inc.php
$cfg['Servers'][$i]['auth_type'] = 'signon';
$cfg['Servers'][$i]['SignonSession'] = 'novapanel_pma_signon';
$cfg['Servers'][$i]['SignonURL'] = '/phpmyadmin/signon';
$cfg['Servers'][$i]['LogoutURL'] = '/dashboard';
```

### How Signon Works

1. User clicks "phpMyAdmin" link in panel
2. Request goes to `/phpmyadmin/signon` (handled by panel)
3. Panel controller checks user authentication
4. If authenticated, panel sets signon session with MySQL credentials
5. User is redirected to `/phpmyadmin/`
6. phpMyAdmin reads signon session and logs in automatically
7. No password prompt shown to user

### Security Configuration

```php
$cfg['blowfish_secret'] = 'randomly_generated_32_chars';
$cfg['Servers'][$i]['host'] = 'localhost';
$cfg['Servers'][$i]['AllowNoPassword'] = false;
```

- Unique blowfish secret per installation
- Only allows authenticated connections
- No anonymous access permitted

## Comparison: NovaPanel vs Traditional Setup

### Traditional phpMyAdmin + Nginx Setup (DigitalOcean Tutorial)

The DigitalOcean tutorial requires:
1. Install phpMyAdmin
2. Create symbolic link
3. Configure Nginx location block
4. Set up authentication (HTTP auth or cookie)
5. Manually enter credentials each time

### NovaPanel Implementation

NovaPanel simplifies this:
1. ✅ **Automatic Installation** - One command installs everything
2. ✅ **Pre-configured** - Nginx configuration automatically created
3. ✅ **SSO Enabled** - No repetitive password entry
4. ✅ **Integrated** - Seamlessly works with panel authentication
5. ✅ **Secure by Default** - Best practices applied automatically

## Addressing Common Concerns

### "Will this install Apache?"

**No.** NovaPanel never installs Apache. The installation script:
- Only installs `nginx` package
- Only installs `php8.2-fpm` for PHP processing
- Only installs `phpmyadmin` package (PHP files, no web server)
- phpMyAdmin package may suggest Apache, but it's not installed

### "What if I already have Apache?"

If you have Apache already installed:
- NovaPanel will not use it
- Nginx listens on port 7080 (panel)
- Apache can continue on port 80/443 for other sites
- No conflicts unless you configure them on same ports

**Recommendation:** For a clean NovaPanel installation, don't install Apache.

### "Will there be port conflicts?"

**No port conflicts:**
- NovaPanel Nginx: Port 7080 (panel + phpMyAdmin)
- Hosted sites: Ports 80 and 443 (Nginx)
- No Apache = No port 80/443 conflicts

### "How is PHP processed without Apache mod_php?"

phpMyAdmin PHP files are processed by **PHP-FPM** (FastCGI Process Manager):
- Nginx forwards PHP requests to PHP-FPM via Unix socket
- More efficient than Apache mod_php
- Better security isolation
- Industry standard for Nginx + PHP

## File Locations

### phpMyAdmin Files
- **Application**: `/usr/share/phpmyadmin/`
- **Config**: `/etc/phpmyadmin/config.inc.php`
- **Symlink**: `/usr/share/phpmyadmin/config.inc.php` → `/etc/phpmyadmin/config.inc.php`

### Nginx Configuration
- **Panel Config**: `/etc/nginx/sites-available/novapanel.conf`
- **Enabled**: `/etc/nginx/sites-enabled/novapanel.conf` (symlink)

### PHP-FPM
- **Socket**: `/var/run/php/php8.2-fpm.sock`
- **Config**: `/etc/php/8.2/fpm/pool.d/www.conf`

## Verification

After installation, you can verify the setup using the provided verification script:

```bash
sudo bash /opt/novapanel/scripts/verify-phpmyadmin.sh
```

This script checks:
- ✅ Web server configuration (Nginx only, no Apache)
- ✅ phpMyAdmin installation
- ✅ Nginx configuration for phpMyAdmin
- ✅ PHP-FPM setup
- ✅ Port configuration
- ✅ HTTP accessibility

## Access Methods

Users can access phpMyAdmin through:

1. **Sidebar Link**: Click "phpMyAdmin" in navigation
2. **Databases Page**: Header button or per-database "Manage" links
3. **Direct URL**: `http://your-server:7080/phpmyadmin/signon`

All methods use SSO - no password entry required!

## Security Considerations

### ✅ Protected by Panel Authentication
- Users must be logged into NovaPanel first
- Signon endpoint requires authentication
- No direct phpMyAdmin access without panel login

### ✅ Secure Credential Handling
- MySQL credentials stored in `.env.php` (not public)
- File permissions: 640 (owner + www-data group only)
- Credentials never exposed to browser
- Loaded server-side only

### ✅ Nginx Security
- phpMyAdmin served through same Nginx as panel
- Same security headers and configurations
- Protected by firewall rules (port 7080)
- SSL/TLS when enabled for panel

### ✅ Session Security
- Uses panel's existing session management
- Session timeout enforced
- No persistent credential storage in browser

## Troubleshooting

### Issue: 404 Not Found on /phpmyadmin

**Cause**: phpMyAdmin not installed or Nginx config missing

**Solution**:
```bash
# Check if phpMyAdmin is installed
dpkg -l | grep phpmyadmin

# Check if files exist
ls -la /usr/share/phpmyadmin/

# Verify Nginx config includes phpMyAdmin location
grep -A10 "location /phpmyadmin" /etc/nginx/sites-available/novapanel.conf

# Test Nginx configuration
sudo nginx -t

# Reload Nginx
sudo systemctl reload nginx
```

### Issue: PHP Files Download Instead of Execute

**Cause**: PHP-FPM not configured correctly in Nginx

**Solution**:
```bash
# Verify PHP-FPM is running
sudo systemctl status php8.2-fpm

# Check socket exists
ls -la /var/run/php/php8.2-fpm.sock

# Verify Nginx config has fastcgi_pass directive
grep "fastcgi_pass" /etc/nginx/sites-available/novapanel.conf
```

### Issue: Blank Page or Errors in phpMyAdmin

**Cause**: Config file missing or incorrect

**Solution**:
```bash
# Check config exists
ls -la /etc/phpmyadmin/config.inc.php

# Verify symlink
ls -la /usr/share/phpmyadmin/config.inc.php

# Check PHP error log
sudo tail -f /var/log/php8.2-fpm.log
```

### Issue: Cannot Login / Access Denied

**Cause**: MySQL credentials incorrect or user doesn't exist

**Solution**:
```bash
# Check credentials in .env.php
sudo cat /opt/novapanel/.env.php | grep MYSQL

# Test MySQL connection
mysql -u novapanel_db -p
# Enter password from .env.php

# Verify MySQL user privileges
mysql -u root -p
mysql> SELECT User, Host FROM mysql.user WHERE User='novapanel_db';
mysql> SHOW GRANTS FOR 'novapanel_db'@'localhost';
```

## Advantages of NovaPanel's Approach

### vs Apache + phpMyAdmin
- ✅ No Apache installation required
- ✅ No web server conflicts
- ✅ Better performance (Nginx)
- ✅ Simpler architecture
- ✅ Less memory usage

### vs Manual Nginx + phpMyAdmin Setup
- ✅ Automatic installation
- ✅ No manual configuration needed
- ✅ SSO pre-configured
- ✅ Integrated with panel authentication
- ✅ Consistent security settings

### vs Other Control Panels
- ✅ No separate web server for admin panel
- ✅ Single port for panel and phpMyAdmin
- ✅ Open source implementation
- ✅ Simple to understand and modify
- ✅ No vendor lock-in

## Best Practices

### For Production Use

1. **Enable SSL/TLS**
   ```bash
   # Get SSL certificate (e.g., Let's Encrypt)
   # Configure Nginx to use HTTPS
   # Sessions will automatically use secure flag
   ```

2. **Restrict Access by IP** (Optional)
   ```nginx
   location /phpmyadmin {
       # Only allow from specific IPs
       allow 192.168.1.0/24;
       allow 10.0.0.0/8;
       deny all;
       
       alias /usr/share/phpmyadmin;
       # ... rest of config
   }
   ```

3. **Monitor Access**
   ```bash
   # Check Nginx access logs
   tail -f /var/log/nginx/access.log | grep phpmyadmin
   ```

4. **Keep Updated**
   ```bash
   # Regularly update phpMyAdmin
   sudo apt-get update
   sudo apt-get upgrade phpmyadmin
   ```

## Alternative: Without SSO

If you prefer manual login (without SSO), modify the config:

```php
// Change in /etc/phpmyadmin/config.inc.php
// From:
$cfg['Servers'][$i]['auth_type'] = 'signon';

// To:
$cfg['Servers'][$i]['auth_type'] = 'cookie';
```

Then users will see phpMyAdmin's login screen and enter credentials manually.

## Conclusion

NovaPanel handles phpMyAdmin installation securely and efficiently using **Nginx only**:

- ✅ No Apache required
- ✅ No web server conflicts
- ✅ Automatic configuration
- ✅ SSO for seamless experience
- ✅ Production-ready security

The implementation follows industry best practices while providing a simpler, more integrated solution than traditional manual setups.

## References

- [phpMyAdmin Documentation](https://docs.phpmyadmin.net/)
- [Nginx FastCGI Documentation](http://nginx.org/en/docs/http/ngx_http_fastcgi_module.html)
- [PHP-FPM Configuration](https://www.php.net/manual/en/install.fpm.php)
- [NovaPanel Security Documentation](../SECURITY.md)
- [NovaPanel Architecture](../DESIGN.md)

---

**For support or questions:**
- GitHub Issues: https://github.com/md-riaz/NovaPanel/issues
- Documentation: https://github.com/md-riaz/NovaPanel/wiki
