#!/bin/bash
# Pure-FTPd Configuration Verification Script
# This script verifies that Pure-FTPd is properly configured for passive mode

echo "================================================"
echo "Pure-FTPd Configuration Verification"
echo "================================================"
echo ""

# Check if Pure-FTPd is installed
echo "1. Checking if Pure-FTPd is installed..."
if command -v pure-pw &> /dev/null; then
    echo "   ✓ Pure-FTPd is installed"
else
    echo "   ✗ Pure-FTPd is NOT installed"
    echo "   Please install it with: sudo apt-get install pure-ftpd"
    exit 1
fi
echo ""

# Check if Pure-FTPd service is running
echo "2. Checking Pure-FTPd service status..."
if systemctl is-active --quiet pure-ftpd; then
    echo "   ✓ Pure-FTPd service is running"
else
    echo "   ✗ Pure-FTPd service is NOT running"
    echo "   Start it with: sudo systemctl start pure-ftpd"
fi
echo ""

# Check configuration files
echo "3. Checking configuration files..."

# PureDB
if [ -f /etc/pure-ftpd/conf/PureDB ]; then
    echo "   ✓ PureDB configuration exists"
    echo "     Content: $(cat /etc/pure-ftpd/conf/PureDB)"
else
    echo "   ✗ PureDB configuration missing"
    echo "     Expected: /etc/pure-ftpd/pureftpd.pdb"
fi

# PassivePortRange
if [ -f /etc/pure-ftpd/conf/PassivePortRange ]; then
    echo "   ✓ PassivePortRange configuration exists"
    echo "     Content: $(cat /etc/pure-ftpd/conf/PassivePortRange)"
else
    echo "   ✗ PassivePortRange configuration missing"
    echo "     Expected: 30000 30100"
fi

# ChrootEveryone
if [ -f /etc/pure-ftpd/conf/ChrootEveryone ]; then
    echo "   ✓ ChrootEveryone configuration exists"
    echo "     Content: $(cat /etc/pure-ftpd/conf/ChrootEveryone)"
else
    echo "   ⚠ ChrootEveryone configuration missing (optional but recommended)"
fi

# NoAnonymous
if [ -f /etc/pure-ftpd/conf/NoAnonymous ]; then
    echo "   ✓ NoAnonymous configuration exists"
    echo "     Content: $(cat /etc/pure-ftpd/conf/NoAnonymous)"
else
    echo "   ⚠ NoAnonymous configuration missing (optional but recommended)"
fi

# Umask
if [ -f /etc/pure-ftpd/conf/Umask ]; then
    echo "   ✓ Umask configuration exists"
    echo "     Content: $(cat /etc/pure-ftpd/conf/Umask)"
else
    echo "   ⚠ Umask configuration missing (optional)"
fi
echo ""

# Check firewall
echo "4. Checking firewall configuration..."
if command -v ufw &> /dev/null; then
    echo "   UFW firewall detected"
    
    # Check FTP control port
    if sudo ufw status | grep -q "21/tcp.*ALLOW"; then
        echo "   ✓ Port 21/tcp is allowed (FTP control)"
    else
        echo "   ✗ Port 21/tcp is NOT allowed"
        echo "     Add it with: sudo ufw allow 21/tcp"
    fi
    
    # Check passive mode ports
    if sudo ufw status | grep -q "30000:30100/tcp.*ALLOW"; then
        echo "   ✓ Ports 30000:30100/tcp are allowed (FTP passive mode)"
    else
        echo "   ✗ Ports 30000:30100/tcp are NOT allowed"
        echo "     Add them with: sudo ufw allow 30000:30100/tcp"
    fi
else
    echo "   ⚠ UFW is not installed or not available"
    echo "     Ensure your firewall allows:"
    echo "     - Port 21/tcp"
    echo "     - Ports 30000:30100/tcp"
fi
echo ""

# Check if PureDB exists
echo "5. Checking FTP user database..."
if [ -f /etc/pure-ftpd/pureftpd.pdb ]; then
    echo "   ✓ PureDB database file exists"
    echo "   FTP Users:"
    sudo pure-pw list 2>/dev/null || echo "     (No users found or error listing)"
else
    echo "   ⚠ PureDB database file does not exist yet"
    echo "     This is normal if no FTP users have been created"
fi
echo ""

# Check ports are listening
echo "6. Checking listening ports..."
if netstat -tuln 2>/dev/null | grep -q ":21 " || ss -tuln 2>/dev/null | grep -q ":21 "; then
    echo "   ✓ Port 21 is listening"
else
    echo "   ✗ Port 21 is NOT listening"
    echo "     Pure-FTPd may not be running properly"
fi
echo ""

# Summary
echo "================================================"
echo "Verification Summary"
echo "================================================"
echo ""
echo "If all checks passed (✓), your FTP server should work with FileZilla."
echo "If any checks failed (✗), follow the suggestions above to fix them."
echo ""
echo "To create an FTP user through the panel:"
echo "  1. Go to NovaPanel → FTP Users → Create FTP User"
echo "  2. Fill in the form with username, password, and home directory"
echo "  3. Test connection with FileZilla using your server IP and credentials"
echo ""
echo "For more information, see: docs/FTP_SETUP.md"
echo "================================================"
