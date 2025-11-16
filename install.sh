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
mkdir -p storage/logs storage/cache storage/uploads
chown -R novapanel:novapanel storage
chmod -R 750 storage
echo "✓ Storage directories configured"
echo ""

# Run database migration
echo "Running database migration..."
sudo -u novapanel php database/migration.php
echo "✓ Database migration completed"
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
echo "Next steps:"
echo "1. Review security settings in SECURITY.md"
echo "2. Set up SSL certificate for port 7080 (optional)"
echo "3. Configure firewall rules for production use"
echo ""
echo "For support, visit: https://github.com/md-riaz/NovaPanel"
echo "=========================================="
