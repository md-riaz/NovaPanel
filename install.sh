#!/bin/bash

set -e

echo "=========================================="
echo "NovaPanel Installation Script"
echo "=========================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "❌ Please run as root (use sudo)"
    exit 1
fi

# Detect OS
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$ID
    VER=$VERSION_ID
else
    echo "❌ Cannot detect OS"
    exit 1
fi

echo "✓ Detected OS: $OS $VER"
echo ""

# Check if Ubuntu/Debian
if [ "$OS" != "ubuntu" ] && [ "$OS" != "debian" ]; then
    echo "❌ This installer only supports Ubuntu and Debian"
    exit 1
fi

echo "Installing dependencies..."
echo "=========================="

# Update package lists
apt-get update -qq

# Add PHP repository for Ubuntu (Debian already has PHP 8.2 in default repos)
if [ "$OS" = "ubuntu" ]; then
    # Install prerequisites for adding PPA
    echo "Installing prerequisites for PPA..."
    apt-get install -y software-properties-common ca-certificates lsb-release apt-transport-https
    
    # Add Ondřej Surý PPA for PHP 8.2
    echo "Adding PHP repository..."
    add-apt-repository ppa:ondrej/php -y
    
    # Update package lists again after adding PPA
    apt-get update -qq
fi

# Pre-configure phpMyAdmin to avoid interactive prompts
# This tells the installer:
# 1. Don't configure any web server automatically (we'll do it manually with Nginx)
# 2. Don't configure database with dbconfig-common (we'll do it manually)
echo "Preparing phpMyAdmin installation (non-interactive)..."
export DEBIAN_FRONTEND=noninteractive
debconf-set-selections <<< "phpmyadmin phpmyadmin/reconfigure-webserver multiselect"
debconf-set-selections <<< "phpmyadmin phpmyadmin/dbconfig-install boolean false"

# Install required packages
apt-get install -y \
    nginx \
    php8.2 \
    php8.2-fpm \
    php8.2-cli \
    php8.2-common \
    php8.2-sqlite3 \
    php8.2-mysql \
    php8.2-pgsql \
    php8.2-curl \
    php8.2-mbstring \
    php8.2-xml \
    composer \
    sqlite3 \
    mysql-server \
    phpmyadmin \
    pure-ftpd \
    bind9 \
    bind9utils \
    git \
    curl \
    unzip

# Reset to normal mode
unset DEBIAN_FRONTEND

# Install ttyd for web terminal (optional but recommended)
echo "Installing ttyd for web terminal..."
if ! command -v ttyd &> /dev/null; then
    # Try to install from package manager first
    if apt-cache show ttyd &> /dev/null; then
        apt-get install -y ttyd
        echo "✓ ttyd installed from package"
    else
        # Download binary if package not available
        echo "Downloading ttyd binary..."
        TTYD_VERSION="1.7.4"
        wget -q https://github.com/tsl0922/ttyd/releases/download/${TTYD_VERSION}/ttyd.x86_64 -O /tmp/ttyd
        if [ -f /tmp/ttyd ]; then
            mv /tmp/ttyd /usr/local/bin/ttyd
            chmod +x /usr/local/bin/ttyd
            echo "✓ ttyd binary installed"
        else
            echo "⚠ Warning: Could not install ttyd. Web terminal feature will not be available."
            echo "  You can install it manually later following the instructions in the panel."
        fi
    fi
else
    echo "✓ ttyd already installed"
fi

echo "✓ Dependencies installed"
echo ""

# Create dedicated PHP-FPM pool for NovaPanel
PHP_FPM_POOL_CONF="/etc/php/8.2/fpm/pool.d/novapanel.conf"
if [ ! -f "$PHP_FPM_POOL_CONF" ]; then
    echo "Creating PHP-FPM pool for NovaPanel..."
    cat > "$PHP_FPM_POOL_CONF" <<EOF
[novaPanel]
user = novapanel
group = www-data
listen = /var/run/php/php8.2-fpm-novapanel.sock
pm = dynamic
pm.max_children = 10
pm.start_servers = 2
pm.min_spare_servers = 1
pm.max_spare_servers = 3
chdir = /
catch_workers_output = yes
php_admin_value[open_basedir] = /opt/novapanel/:/usr/share/phpmyadmin/:/tmp/:/etc/nginx/:/etc/php/:/var/run/php/:/usr/bin/:/bin/
EOF
    systemctl restart php8.2-fpm
    echo "✓ PHP-FPM pool created and restarted"
else
    echo "✓ PHP-FPM pool for NovaPanel already exists"
fi

# Configure phpMyAdmin
echo "Configuring phpMyAdmin..."
echo "Note: NovaPanel uses Nginx only (no Apache) for phpMyAdmin"
echo "      phpMyAdmin will be served through Nginx on port 7080"
# During phpMyAdmin installation, it asks for web server configuration
# We'll configure it manually through Nginx instead (no Apache needed)
# Create phpMyAdmin config directory if it doesn't exist
mkdir -p /etc/phpmyadmin
# Create a basic config file for phpMyAdmin with SSO (signon) authentication
if [ ! -f /etc/phpmyadmin/config.inc.php ]; then
    # Generate a secure blowfish secret
    BLOWFISH_SECRET=$(openssl rand -base64 32 | tr -d "=+/" | cut -c1-32)
    cat > /etc/phpmyadmin/config.inc.php <<PMAEOF
<?php
/* phpMyAdmin configuration for NovaPanel - SSO enabled */
\$cfg['blowfish_secret'] = '${BLOWFISH_SECRET}';
\$i = 0;
\$i++;
/* Use signon authentication for automatic login from NovaPanel */
\$cfg['Servers'][\$i]['auth_type'] = 'signon';
\$cfg['Servers'][\$i]['SignonSession'] = 'novapanel_pma_signon';
\$cfg['Servers'][\$i]['SignonURL'] = '/phpmyadmin/signon';
\$cfg['Servers'][\$i]['LogoutURL'] = '/dashboard';
\$cfg['Servers'][\$i]['host'] = 'localhost';
\$cfg['Servers'][\$i]['compress'] = false;
\$cfg['Servers'][\$i]['AllowNoPassword'] = false;
\$cfg['UploadDir'] = '';
\$cfg['SaveDir'] = '';
PMAEOF
    chmod 644 /etc/phpmyadmin/config.inc.php
fi
# Ensure phpMyAdmin can use the config
if [ -d /usr/share/phpmyadmin ]; then
    ln -sf /etc/phpmyadmin/config.inc.php /usr/share/phpmyadmin/config.inc.php 2>/dev/null || true
    echo "✓ phpMyAdmin configured"
else
    echo "⚠ Warning: phpMyAdmin directory not found. It will be installed during apt-get install."
fi
echo ""

# Configure Pure-FTPd
echo "Configuring Pure-FTPd..."
# Enable PureDB authentication
if [ ! -f /etc/pure-ftpd/conf/PureDB ]; then
    echo "/etc/pure-ftpd/pureftpd.pdb" > /etc/pure-ftpd/conf/PureDB
fi
# Ensure Pure-FTPd service is enabled and started
systemctl enable pure-ftpd 2>/dev/null || true
systemctl restart pure-ftpd 2>/dev/null || true
echo "✓ Pure-FTPd configured"
echo ""

# Create panel user
echo "Creating panel user..."
if id "novapanel" &>/dev/null; then
    echo "✓ User 'novapanel' already exists"
else
    useradd -r -m -d /opt/novapanel -s /bin/bash novapanel
    echo "✓ Created user 'novapanel'"
fi
echo ""

# Set up panel directory
PANEL_DIR="/opt/novapanel"
CURRENT_DIR=$(pwd)

echo "Setting up panel..."
if [ "$CURRENT_DIR" != "$PANEL_DIR" ]; then
    if [ -d "$PANEL_DIR" ]; then
        echo "Existing panel directory detected at $PANEL_DIR"
        echo "Removing existing contents in $PANEL_DIR before copying (services left running)..."
        rm -rf "${PANEL_DIR}/"* "${PANEL_DIR}/".[!.]* "${PANEL_DIR}/"..?* 2>/dev/null || true
    fi

    mkdir -p "$PANEL_DIR"
    echo "Copying project files to $PANEL_DIR..."
    cp -a "$CURRENT_DIR"/. "$PANEL_DIR"/
    rm -rf "$PANEL_DIR/.git"
    cd "$PANEL_DIR"
fi

# Set proper ownership and permissions for panel files
echo "Setting file ownership and permissions..."
# Set ownership to novapanel:www-data so PHP-FPM (running as www-data) can read files
chown -R novapanel:www-data $PANEL_DIR
# Set secure permissions: directories 755, files 644
find $PANEL_DIR -type d -exec chmod 755 {} \;
find $PANEL_DIR -type f -exec chmod 644 {} \;
echo "✓ File ownership and permissions configured"
echo ""

# Install Composer dependencies
echo "Installing PHP dependencies..."
sudo -u novapanel composer install --no-dev --optimize-autoloader
echo "✓ PHP dependencies installed"
echo ""

# Set up storage directories
echo "Setting up storage directories..."
mkdir -p storage/logs storage/cache storage/uploads storage/terminal/pids storage/terminal/logs
chown -R novapanel:www-data storage
chmod -R 775 storage
echo "✓ Storage directories configured"
echo ""

# Run database migration
# NOTE: The panel.db file is automatically created by SQLite when the migration script
# connects to the database for the first time. This happens inside Database::panel()
# when it calls: new PDO("sqlite:$dbPath")
echo "Running database migration..."
sudo -u novapanel php8.2 database/migration.php
# Ensure database file is writable by www-data group
if [ -f storage/panel.db ]; then
    chown novapanel:www-data storage/panel.db
    chmod 660 storage/panel.db
fi
echo "✓ Database migration completed"
echo ""

# Create MySQL user for panel database management
echo "Creating MySQL user for panel..."
MYSQL_PANEL_USER="novapanel_db"
MYSQL_PANEL_PASS=$(openssl rand -base64 32 | tr -d "=+/" | cut -c1-25)

# Check if the MySQL user already exists so we can reset its password
MYSQL_USER_EXISTS=$(mysql -u root -N -s -e "SELECT EXISTS(SELECT 1 FROM mysql.user WHERE user='${MYSQL_PANEL_USER}' AND host='localhost');")

if [ "$MYSQL_USER_EXISTS" = "1" ]; then
    echo "✓ MySQL user '${MYSQL_PANEL_USER}' already exists — resetting password"
else
    echo "✓ MySQL user '${MYSQL_PANEL_USER}' does not exist — creating user"
fi

# Create or update the MySQL user with the generated password and ensure permissions are set
mysql -u root <<MYSQL_EOF
CREATE USER IF NOT EXISTS '${MYSQL_PANEL_USER}'@'localhost' IDENTIFIED BY '${MYSQL_PANEL_PASS}';
ALTER USER '${MYSQL_PANEL_USER}'@'localhost' IDENTIFIED BY '${MYSQL_PANEL_PASS}';
GRANT ALL PRIVILEGES ON *.* TO '${MYSQL_PANEL_USER}'@'localhost' WITH GRANT OPTION;
FLUSH PRIVILEGES;
MYSQL_EOF

echo "✓ MySQL user '${MYSQL_PANEL_USER}' created with database management privileges"
echo ""

# Configure BIND9 for DNS management
echo "BIND9 Setup (for DNS management)"
echo "================================"

# Create zones directory
mkdir -p /etc/bind/zones
chown bind:bind /etc/bind/zones
chmod 755 /etc/bind/zones

# Create named.conf.local if it doesn't exist
if [ ! -f /etc/bind/named.conf.local ]; then
    cat > /etc/bind/named.conf.local <<'BIND_CONF'
// Local zones configuration for NovaPanel
// Zone files are managed automatically by the panel

BIND_CONF
    chown root:bind /etc/bind/named.conf.local
    chmod 644 /etc/bind/named.conf.local
fi

# Configure BIND9 options
cat > /etc/bind/named.conf.options <<'BIND_OPTIONS'
options {
    directory "/var/cache/bind";

    // Allow queries from any host
    allow-query { any; };

    // Disable recursion for security (authoritative DNS only)
    recursion no;

    // Listen on all interfaces
    listen-on { any; };
    listen-on-v6 { any; };

    // DNSSEC validation
    dnssec-validation auto;
};
BIND_OPTIONS

# Verify BIND configuration
if named-checkconf; then
    echo "✓ BIND9 configuration valid"
else
    echo "❌ BIND9 configuration error"
    exit 1
fi

# Enable and start BIND9
# Note: On some systems, bind9 is a symlink/alias to named.service
# Try to enable it, but don't fail if it's already enabled via another name
if systemctl is-enabled bind9 >/dev/null 2>&1 || systemctl is-enabled named >/dev/null 2>&1; then
    echo "✓ BIND9 service already enabled"
else
    # Try to enable, but continue even if it fails due to symlink issues
    if ! systemctl enable bind9 2>/dev/null; then
        echo "⚠ Note: Could not enable bind9 directly (may be a service alias)"
        echo "  Attempting to enable via 'named' service name..."
        systemctl enable named 2>/dev/null || true
    fi
fi

# Start/restart BIND9 - this is the critical part
systemctl restart bind9 || systemctl restart named

echo "✓ BIND9 installed and configured"
echo ""

# Create configuration file
echo "Creating configuration file..."

cat > $PANEL_DIR/.env.php <<ENVEOF
<?php
// NovaPanel Configuration
// Generated during installation

// MySQL Credentials (for creating user databases)
// These credentials are used by the panel to create and manage customer databases
putenv('MYSQL_HOST=localhost');
putenv('MYSQL_ROOT_USER=${MYSQL_PANEL_USER}');
putenv('MYSQL_ROOT_PASSWORD=${MYSQL_PANEL_PASS}');

// PostgreSQL Credentials (not installed by default - leave empty)
// Install PostgreSQL separately if needed and update these values
putenv('PGSQL_HOST=');
putenv('PGSQL_ROOT_USER=');
putenv('PGSQL_ROOT_PASSWORD=');

// BIND9 Configuration (for DNS management)
putenv('BIND9_ZONES_PATH=/etc/bind/zones');
putenv('BIND9_NAMED_CONF_PATH=/etc/bind/named.conf.local');

// Application
putenv('APP_ENV=production');
putenv('APP_DEBUG=false');

// Detect server IP during installation
// This will be updated with the actual server IP below
ENVEOF

# Detect server IP and add to config
SERVER_IP=$(hostname -I | awk '{print $1}')
echo "putenv('APP_URL=http://${SERVER_IP}:7080');" >> $PANEL_DIR/.env.php

chown novapanel:www-data $PANEL_DIR/.env.php
chmod 640 $PANEL_DIR/.env.php
echo "✓ Configuration file created"
echo ""

# Create admin user
echo "Creating admin user..."
ADMIN_USER="admin"
ADMIN_EMAIL="admin@novapanel.com"
ADMIN_PASS=$(openssl rand -base64 32 | tr -d "=+/" | cut -c1-25)

ADMIN_PASS_HASH=$(php8.2 -r "echo password_hash('$ADMIN_PASS', PASSWORD_DEFAULT);")

sudo -u novapanel sqlite3 storage/panel.db <<EOF
INSERT INTO users (username, email, password) 
VALUES ('$ADMIN_USER', '$ADMIN_EMAIL', '$ADMIN_PASS_HASH');

INSERT INTO user_roles (user_id, role_id) 
SELECT 
    (SELECT id FROM users WHERE username = '$ADMIN_USER'),
    (SELECT id FROM roles WHERE name = 'Admin');
EOF

echo "✓ Admin user created"
echo ""

# Set up sudoers
echo "Configuring sudo permissions..."
cat > /etc/sudoers.d/novapanel <<'EOF'
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
EOF
chmod 440 /etc/sudoers.d/novapanel

# Validate sudoers file
if visudo -c -f /etc/sudoers.d/novapanel > /dev/null 2>&1; then
    echo "✓ Sudo permissions configured and validated"
else
    echo "❌ Error: sudoers file validation failed"
    echo "   Please check /etc/sudoers.d/novapanel for syntax errors"
    exit 1
fi
echo ""

# Configure Nginx for panel
echo "Configuring Nginx..."
echo "✓ Using server IP: $SERVER_IP"

cat > /etc/nginx/sites-available/novapanel.conf <<EOF
server {
    listen 7080;
    server_name $SERVER_IP _;
    root $PANEL_DIR/public;
    index index.php;

    location / {
        try_files \$uri \$uri/ /index.php?\$query_string;
    }

    # phpMyAdmin location
    location /phpmyadmin {
        alias /usr/share/phpmyadmin;
        index index.php;

        location ~ ^/phpmyadmin/(.+\.php)$ {
            alias /usr/share/phpmyadmin/\$1;
            fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
            fastcgi_index index.php;
            fastcgi_param SCRIPT_FILENAME \$request_filename;
            include fastcgi_params;
        }

        location ~* ^/phpmyadmin/(.+\.(jpg|jpeg|gif|css|png|js|ico|html|xml|txt))$ {
            alias /usr/share/phpmyadmin/\$1;
        }
    }

    location ~ \.php$ {
        fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME \$document_root\$fastcgi_script_name;
        include fastcgi_params;
    }


    # Secure ttyd terminal proxy with session-based auth
    location /ttyd/ {
        auth_request /auth_check;
        error_page 401 = /login.php;
        proxy_pass http://127.0.0.1:7681;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 86400;
    }

    # Internal auth check endpoint for Nginx
    location = /auth_check {
        include fastcgi_params;
        fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
        fastcgi_param SCRIPT_FILENAME $document_root/auth_check.php;
        fastcgi_param HTTP_COOKIE $http_cookie;
    }

    location ~ /\.ht {
        deny all;
    }
}
EOF

ln -sf /etc/nginx/sites-available/novapanel.conf /etc/nginx/sites-enabled/
nginx -t
systemctl reload nginx

echo "✓ Nginx configured"
echo ""

# Set up firewall (optional)
if command -v ufw &> /dev/null; then
    echo "Configuring firewall..."
    ufw --force enable
    ufw allow 22/tcp
    ufw allow 80/tcp
    ufw allow 443/tcp
    ufw allow 7080/tcp
    ufw allow 21/tcp
    echo "✓ Firewall configured"
    echo ""
fi

echo "=========================================="
echo "✅ NovaPanel Installation Complete!"
echo "=========================================="
echo ""
echo "Access your panel at: http://$SERVER_IP:7080"
echo ""
echo "Admin Credentials:"
echo "  Username: $ADMIN_USER"
echo "  Email: $ADMIN_EMAIL"
echo "  Password: $ADMIN_PASS"
echo ""
echo "⚠️  IMPORTANT: Save these credentials securely!"
echo "   Change the password after first login."
echo ""
echo "Features available:"
echo "  • Site Management"
echo "  • User Management"
echo "  • Database Management"
echo "  • phpMyAdmin (accessible at http://$SERVER_IP:7080/phpmyadmin/signon)"
echo "  • DNS Management"
echo "  • FTP Management"
echo "  • Cron Jobs"
if command -v ttyd &> /dev/null; then
    echo "  • Web Terminal (ttyd installed)"
else
    echo "  • Web Terminal (ttyd not installed - see panel for instructions)"
fi
echo ""
echo "📌 Web Server Architecture:"
echo "  • Nginx only (no Apache installed)"
echo "  • phpMyAdmin served through Nginx on port 7080"
echo "  • All sites use Nginx + PHP-FPM"
echo "  • No web server port conflicts"
echo ""
echo "Next steps:"
echo "1. Review security settings in SECURITY.md"
echo "2. Set up SSL certificate for port 7080 (optional)"
echo "3. Configure firewall rules for production use"
echo ""
echo "To verify phpMyAdmin setup:"
echo "  sudo bash $PANEL_DIR/scripts/verify-phpmyadmin.sh"
echo ""
echo "For support, visit: https://github.com/md-riaz/NovaPanel"
echo "=========================================="
