#!/bin/bash

# Setup sudoers configuration for NovaPanel
# This script must be run as root

set -e

echo "=========================================="
echo "NovaPanel Sudoers Setup"
echo "=========================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "❌ This script must be run as root"
    echo "   Please run: sudo bash $0"
    exit 1
fi

# Check if novapanel user exists
if ! id "novapanel" &>/dev/null; then
    echo "⚠️  Warning: User 'novapanel' does not exist"
    echo "   Creating novapanel user..."
    useradd -r -m -d /opt/novapanel -s /bin/bash novapanel
    echo "✓ Created user 'novapanel'"
    echo ""
fi

# Create sudoers configuration
echo "Configuring sudo permissions for novapanel user..."
cat > /etc/sudoers.d/novapanel <<'EOF'
# NovaPanel Sudoers Configuration
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
echo "Validating sudoers configuration..."
if visudo -c -f /etc/sudoers.d/novapanel > /dev/null 2>&1; then
    echo "✓ Sudoers file validated successfully"
else
    echo "❌ Error: Sudoers file validation failed"
    echo "   Please check /etc/sudoers.d/novapanel for syntax errors"
    rm -f /etc/sudoers.d/novapanel
    exit 1
fi

echo ""
echo "=========================================="
echo "✅ Sudoers configuration complete!"
echo "=========================================="
echo ""
echo "The novapanel user can now run sudo commands without a password."
echo "Configuration file: /etc/sudoers.d/novapanel"
echo ""
echo "You can now use NovaPanel without sudo password prompts."
echo ""
