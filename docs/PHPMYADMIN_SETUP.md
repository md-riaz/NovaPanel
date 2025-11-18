# phpMyAdmin Integration in NovaPanel

## Overview

NovaPanel includes **phpMyAdmin** integration, providing users with a familiar web-based interface to manage their MySQL databases directly from the panel.

**Important:** phpMyAdmin is served through **Nginx only** - no Apache web server is required or installed. This prevents port conflicts and simplifies the architecture.

## Features

- **Nginx Only**: No Apache required - everything runs through Nginx
- **Direct Access**: phpMyAdmin is accessible directly from the NovaPanel interface
- **Convenient Links**: Access phpMyAdmin from:
  - Sidebar navigation
  - Databases page (top-right button)
  - Per-database "Manage" links
- **Secure Access**: phpMyAdmin is served through the same Nginx server as the panel (port 7080)
- **Single Sign-On (SSO)**: Automatic login with your NovaPanel session - no password entry needed

## Installation

phpMyAdmin is automatically installed and configured when you run the NovaPanel installation script:

```bash
sudo bash install.sh
```

The installer will:
1. Install phpMyAdmin package via apt-get (PHP files only, no web server)
2. Create phpMyAdmin configuration file with SSO enabled
3. Configure Nginx to serve phpMyAdmin at `/phpmyadmin` 
4. Set up PHP-FPM processing (no Apache mod_php)
5. Configure proper permissions and security settings

**Note:** During phpMyAdmin package installation, if asked to select a web server, choose **"None"** - NovaPanel configures Nginx manually for better integration.

## Access Points

### 1. Sidebar Navigation
A dedicated "phpMyAdmin" link appears in the main navigation sidebar for quick access.

### 2. Databases Page
- **Top Button**: Click "phpMyAdmin" button to open phpMyAdmin in a new tab
- **Per-Database Links**: Each database has a "Manage" button that opens phpMyAdmin with that specific database pre-selected

### 3. Direct URL
Access phpMyAdmin directly at: `http://your-server-ip:7080/phpmyadmin`

## Usage

1. **Navigate** to Databases page or click phpMyAdmin in the sidebar
2. **Click** the phpMyAdmin button or a database's "Manage" link
3. **Automatic Login**: You'll be instantly logged in with full database access - no credentials needed!
4. **Manage** your databases through the phpMyAdmin interface

### Single Sign-On (SSO) Feature

NovaPanel implements automatic login to phpMyAdmin:
- **No password entry required** - your panel session automatically authenticates you
- **Direct access** to all databases using the panel's MySQL credentials
- **Seamless experience** - click and go, no additional login screens

## Security Considerations

- **SSO Authentication**: phpMyAdmin uses single sign-on authentication via the panel session
- **Panel Authentication Required**: Users must be logged into NovaPanel to access phpMyAdmin
- **Automatic Credentials**: MySQL credentials are automatically provided from the panel's environment configuration
- **Session-Based**: Uses NovaPanel's existing session management for security
- **No Direct Access**: Direct phpMyAdmin access requires panel authentication first
- Access is served through the same secure Nginx configuration as the panel
- phpMyAdmin config file is protected with appropriate file permissions (644)
- No anonymous access is allowed

## Configuration

### Location
phpMyAdmin configuration is stored at: `/etc/phpmyadmin/config.inc.php`

### Nginx Configuration
The Nginx location block for phpMyAdmin is configured in: `/etc/nginx/sites-available/novapanel.conf`

```nginx
location /phpmyadmin {
    alias /usr/share/phpmyadmin;
    index index.php;
    
    # PHP processing for phpMyAdmin
    location ~ ^/phpmyadmin/(.+\.php)$ {
        alias /usr/share/phpmyadmin/$1;
        fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
        # ... other fastcgi settings
    }
    
    # Static assets
    location ~* ^/phpmyadmin/(.+\.(jpg|jpeg|gif|css|png|js|ico|html|xml|txt))$ {
        alias /usr/share/phpmyadmin/$1;
    }
}
```

## Troubleshooting

### phpMyAdmin Not Accessible

If phpMyAdmin is not accessible after installation:

1. **Check if phpMyAdmin is installed**:
   ```bash
   dpkg -l | grep phpmyadmin
   ```

2. **Verify Nginx configuration**:
   ```bash
   sudo nginx -t
   ```

3. **Check phpMyAdmin files exist**:
   ```bash
   ls -la /usr/share/phpmyadmin/
   ```

4. **Restart Nginx**:
   ```bash
   sudo systemctl restart nginx
   ```

### Cannot Login

If you cannot login to phpMyAdmin:

1. **Verify MySQL is running**:
   ```bash
   sudo systemctl status mysql
   ```

2. **Test MySQL credentials**:
   ```bash
   mysql -u your_username -p
   ```

3. **Check MySQL user permissions**:
   ```sql
   SELECT User, Host FROM mysql.user;
   ```

### 404 Error

If you get a 404 error when accessing phpMyAdmin:

1. Check the Nginx configuration includes the phpMyAdmin location block
2. Verify the phpMyAdmin files are in `/usr/share/phpmyadmin/`
3. Restart Nginx: `sudo systemctl restart nginx`

## Manual Installation (If Needed)

If phpMyAdmin wasn't installed during the initial setup, you can install it manually:

```bash
# Install phpMyAdmin
sudo apt-get update
sudo apt-get install phpmyadmin -y

# Create config file
sudo mkdir -p /etc/phpmyadmin
sudo nano /etc/phpmyadmin/config.inc.php
# Add the configuration from install.sh

# Link config to phpMyAdmin directory
sudo ln -sf /etc/phpmyadmin/config.inc.php /usr/share/phpmyadmin/config.inc.php

# Restart Nginx
sudo systemctl restart nginx
```

## Alternative: Adminer

If you prefer a lighter alternative to phpMyAdmin, you can use Adminer (single PHP file):

```bash
# Download Adminer
cd /opt/novapanel/public
mkdir -p database-manager
cd database-manager
wget https://github.com/vrana/adminer/releases/download/v4.8.1/adminer-4.8.1.php -O index.php

# Access at: http://your-server-ip:7080/database-manager/
```

## Support

For issues or questions about phpMyAdmin integration:
- Check the [NovaPanel Documentation](../README.md)
- Open an issue on [GitHub](https://github.com/md-riaz/NovaPanel/issues)
- Review phpMyAdmin's official documentation at [phpMyAdmin.net](https://www.phpmyadmin.net/)
