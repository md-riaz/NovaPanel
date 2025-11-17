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
    bind9 \
    bind9utils \
    git \
    curl \
    unzip

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
    mkdir -p $PANEL_DIR
    cp -r . $PANEL_DIR/
    cd $PANEL_DIR
fi

# Install Composer dependencies
echo "Installing PHP dependencies..."
sudo -u novapanel composer install --no-dev --optimize-autoloader
echo "✓ PHP dependencies installed"
echo ""

# Set up storage directories
echo "Setting up storage directories..."
mkdir -p storage/logs storage/cache storage/uploads storage/terminal/pids storage/terminal/logs
chown -R novapanel:novapanel storage
chmod -R 750 storage
echo "✓ Storage directories configured"
echo ""

# Run database migration
echo "Running database migration..."
sudo -u novapanel php database/migration.php
echo "✓ Database migration completed"
echo ""

# Create MySQL user for panel database management
echo "Creating MySQL user for panel..."
MYSQL_PANEL_USER="novapanel_db"
MYSQL_PANEL_PASS=$(openssl rand -base64 32 | tr -d "=+/" | cut -c1-25)

# Create MySQL user with necessary privileges for database management
mysql -u root <<MYSQL_EOF
CREATE USER IF NOT EXISTS '${MYSQL_PANEL_USER}'@'localhost' IDENTIFIED BY '${MYSQL_PANEL_PASS}';
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
ENVEOF

chown novapanel:novapanel $PANEL_DIR/.env.php
chmod 600 $PANEL_DIR/.env.php
echo "✓ Configuration file created"
echo ""

# Create admin user
echo "Creating admin user..."
read -p "Enter admin username: " ADMIN_USER
read -p "Enter admin email: " ADMIN_EMAIL
read -s -p "Enter admin password: " ADMIN_PASS
echo ""

ADMIN_PASS_HASH=$(php -r "echo password_hash('$ADMIN_PASS', PASSWORD_DEFAULT);")

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
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/useradd
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/usermod
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/userdel
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
EOF
chmod 440 /etc/sudoers.d/novapanel
echo "✓ Sudo permissions configured"
echo ""

# Configure Nginx for panel
echo "Configuring Nginx..."

# Detect server IP
SERVER_IP=$(hostname -I | awk '{print $1}')
echo "✓ Detected server IP: $SERVER_IP"

cat > /etc/nginx/sites-available/novapanel.conf <<EOF
server {
    listen 7080;
    server_name $SERVER_IP _;
    root $PANEL_DIR/public;
    index index.php;

    location / {
        try_files \$uri \$uri/ /index.php?\$query_string;
    }

    location ~ \.php$ {
        fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME \$document_root\$fastcgi_script_name;
        include fastcgi_params;
    }

    # Proxy for ttyd terminal WebSocket connections (ports 7100-7199)
    # This allows the terminal to be accessed through the panel's main port
    location ~ ^/terminal-ws/([0-9]+)$ {
        set \$terminal_port \$1;
        proxy_pass http://127.0.0.1:\$terminal_port;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_read_timeout 86400;
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
    echo "✓ Firewall configured"
    echo ""
fi

echo "=========================================="
echo "✅ NovaPanel Installation Complete!"
echo "=========================================="
echo ""
echo "Access your panel at: http://$SERVER_IP:7080"
echo "Admin username: $ADMIN_USER"
echo "Admin email: $ADMIN_EMAIL"
echo ""
echo "Features available:"
echo "  • Site Management"
echo "  • User Management"
echo "  • Database Management"
echo "  • DNS Management"
echo "  • FTP Management"
echo "  • Cron Jobs"
if command -v ttyd &> /dev/null; then
    echo "  • Web Terminal (ttyd installed)"
else
    echo "  • Web Terminal (ttyd not installed - see panel for instructions)"
fi
echo ""
echo "Next steps:"
echo "1. Review security settings in SECURITY.md"
echo "2. Set up SSL certificate for port 7080 (optional)"
echo "3. Configure firewall rules for production use"
echo ""
echo "For support, visit: https://github.com/md-riaz/NovaPanel"
echo "=========================================="
