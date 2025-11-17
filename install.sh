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

# Ask if user wants to install PowerDNS for DNS management
echo "PowerDNS Setup (optional - for DNS management)"
echo "=============================================="
read -p "Do you want to install and configure PowerDNS? (y/N): " INSTALL_POWERDNS

POWERDNS_USER=""
POWERDNS_PASS=""
POWERDNS_DB="powerdns"

if [[ "$INSTALL_POWERDNS" =~ ^[Yy]$ ]]; then
    echo "Installing PowerDNS..."
    apt-get install -y pdns-server pdns-backend-mysql
    
    # Create PowerDNS database and user
    POWERDNS_USER="powerdns"
    POWERDNS_PASS=$(openssl rand -base64 32 | tr -d "=+/" | cut -c1-25)
    
    mysql -u root <<PDNS_EOF
CREATE DATABASE IF NOT EXISTS ${POWERDNS_DB};
CREATE USER IF NOT EXISTS '${POWERDNS_USER}'@'localhost' IDENTIFIED BY '${POWERDNS_PASS}';
GRANT ALL PRIVILEGES ON ${POWERDNS_DB}.* TO '${POWERDNS_USER}'@'localhost';
FLUSH PRIVILEGES;

USE ${POWERDNS_DB};

CREATE TABLE IF NOT EXISTS domains (
  id INTEGER PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(255) NOT NULL,
  master VARCHAR(128) DEFAULT NULL,
  last_check INT DEFAULT NULL,
  type VARCHAR(6) NOT NULL,
  notified_serial INT DEFAULT NULL,
  account VARCHAR(40) DEFAULT NULL,
  UNIQUE KEY name_index(name)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS records (
  id INTEGER PRIMARY KEY AUTO_INCREMENT,
  domain_id INT DEFAULT NULL,
  name VARCHAR(255) DEFAULT NULL,
  type VARCHAR(10) DEFAULT NULL,
  content VARCHAR(65535) DEFAULT NULL,
  ttl INT DEFAULT NULL,
  prio INT DEFAULT NULL,
  disabled TINYINT(1) DEFAULT 0,
  ordername VARCHAR(255) DEFAULT NULL,
  auth TINYINT(1) DEFAULT 1,
  KEY domain_id(domain_id),
  KEY name_index(name),
  KEY nametype_index(name,type),
  KEY domain_id_ordername(domain_id, ordername)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS supermasters (
  ip VARCHAR(64) NOT NULL,
  nameserver VARCHAR(255) NOT NULL,
  account VARCHAR(40) NOT NULL,
  PRIMARY KEY(ip, nameserver)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS domainmetadata (
  id INTEGER PRIMARY KEY AUTO_INCREMENT,
  domain_id INT NOT NULL,
  kind VARCHAR(32),
  content TEXT
) ENGINE=InnoDB;
PDNS_EOF
    
    # Configure PowerDNS to use MySQL backend
    cat > /etc/powerdns/pdns.d/mysql.conf <<PDNS_CONF
launch+=gmysql
gmysql-host=localhost
gmysql-dbname=${POWERDNS_DB}
gmysql-user=${POWERDNS_USER}
gmysql-password=${POWERDNS_PASS}
PDNS_CONF
    
    systemctl restart pdns
    systemctl enable pdns
    
    echo "✓ PowerDNS installed and configured"
else
    echo "⊘ Skipping PowerDNS installation"
fi
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

// PowerDNS Database Credentials (for DNS management)
putenv('POWERDNS_HOST=localhost');
putenv('POWERDNS_DATABASE=${POWERDNS_DB}');
putenv('POWERDNS_USER=${POWERDNS_USER}');
putenv('POWERDNS_PASSWORD=${POWERDNS_PASS}');

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
novapanel ALL=(ALL) NOPASSWD: /bin/mkdir
novapanel ALL=(ALL) NOPASSWD: /bin/chown
novapanel ALL=(ALL) NOPASSWD: /bin/chmod
novapanel ALL=(ALL) NOPASSWD: /usr/bin/crontab
novapanel ALL=(ALL) NOPASSWD: /bin/ln
novapanel ALL=(ALL) NOPASSWD: /bin/rm
novapanel ALL=(ALL) NOPASSWD: /bin/cp
novapanel ALL=(ALL) NOPASSWD: /usr/sbin/nginx -t
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
