#!/bin/bash

# NovaPanel phpMyAdmin Verification Script
# This script verifies that phpMyAdmin is correctly installed and configured with Nginx

set -e

echo "=================================================="
echo "NovaPanel phpMyAdmin Verification Script"
echo "=================================================="
echo ""

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Track overall status
ALL_PASSED=true

# Function to print status
print_status() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $2"
    else
        echo -e "${RED}✗${NC} $2"
        ALL_PASSED=false
    fi
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

echo "1. Checking Web Server Configuration"
echo "======================================"

# Check if Apache is NOT installed/running (we want Nginx only)
if systemctl is-active --quiet apache2 2>/dev/null; then
    print_status 1 "Apache is running (WARNING: NovaPanel uses Nginx only)"
    print_warning "Apache should not be running. NovaPanel uses Nginx."
    print_warning "To stop Apache: sudo systemctl stop apache2 && sudo systemctl disable apache2"
else
    print_status 0 "Apache is not running (Good: Nginx-only architecture)"
fi

# Check if Nginx is installed and running
if systemctl is-active --quiet nginx; then
    print_status 0 "Nginx is running"
else
    print_status 1 "Nginx is NOT running"
fi

echo ""
echo "2. Checking phpMyAdmin Installation"
echo "===================================="

# Check if phpMyAdmin package is installed
if dpkg -l | grep -q "^ii.*phpmyadmin"; then
    print_status 0 "phpMyAdmin package is installed"
else
    print_status 1 "phpMyAdmin package is NOT installed"
fi

# Check if phpMyAdmin directory exists
if [ -d "/usr/share/phpmyadmin" ]; then
    print_status 0 "phpMyAdmin directory exists (/usr/share/phpmyadmin)"
else
    print_status 1 "phpMyAdmin directory NOT found"
fi

# Check if phpMyAdmin config exists
if [ -f "/etc/phpmyadmin/config.inc.php" ]; then
    print_status 0 "phpMyAdmin config file exists"
    
    # Check if it's using signon authentication
    if grep -q "auth_type.*signon" /etc/phpmyadmin/config.inc.php; then
        print_status 0 "SSO (signon) authentication configured"
    else
        print_status 1 "SSO authentication NOT configured"
    fi
else
    print_status 1 "phpMyAdmin config file NOT found"
fi

# Check symlink
if [ -L "/usr/share/phpmyadmin/config.inc.php" ]; then
    print_status 0 "Config symlink exists"
else
    print_status 1 "Config symlink NOT found"
fi

echo ""
echo "3. Checking Nginx Configuration"
echo "================================"

# Check if Nginx config file exists
if [ -f "/etc/nginx/sites-available/novapanel.conf" ]; then
    print_status 0 "NovaPanel Nginx config exists"
    
    # Check if it includes phpMyAdmin location block
    if grep -q "location /phpmyadmin" /etc/nginx/sites-available/novapanel.conf; then
        print_status 0 "phpMyAdmin location block configured"
    else
        print_status 1 "phpMyAdmin location block NOT found in Nginx config"
    fi
    
    # Check if it uses PHP-FPM (not Apache mod_php)
    if grep -q "fastcgi_pass.*php.*fpm" /etc/nginx/sites-available/novapanel.conf; then
        print_status 0 "PHP-FPM configured (not Apache mod_php)"
    else
        print_status 1 "PHP-FPM NOT configured properly"
    fi
else
    print_status 1 "NovaPanel Nginx config NOT found"
fi

# Test Nginx configuration
if nginx -t 2>&1 | grep -q "successful"; then
    print_status 0 "Nginx configuration is valid"
else
    print_status 1 "Nginx configuration has errors"
fi

echo ""
echo "4. Checking PHP-FPM"
echo "==================="

# Check if PHP-FPM is running
if systemctl is-active --quiet php8.2-fpm 2>/dev/null || systemctl is-active --quiet php-fpm 2>/dev/null; then
    print_status 0 "PHP-FPM is running"
else
    print_status 1 "PHP-FPM is NOT running"
fi

# Check if PHP-FPM socket exists
if [ -S "/var/run/php/php8.2-fpm.sock" ]; then
    print_status 0 "PHP-FPM socket exists"
elif [ -S "/var/run/php/php-fpm.sock" ]; then
    print_status 0 "PHP-FPM socket exists"
else
    print_status 1 "PHP-FPM socket NOT found"
fi

echo ""
echo "5. Checking Port Configuration"
echo "==============================="

# Check what's listening on port 7080 (should be Nginx)
if netstat -tlnp 2>/dev/null | grep ":7080" | grep -q "nginx"; then
    print_status 0 "Nginx is listening on port 7080"
elif ss -tlnp 2>/dev/null | grep ":7080" | grep -q "nginx"; then
    print_status 0 "Nginx is listening on port 7080"
else
    print_status 1 "Nginx is NOT listening on port 7080"
fi

# Check if Apache is NOT listening on port 7080
if netstat -tlnp 2>/dev/null | grep ":7080" | grep -q "apache"; then
    print_status 1 "Apache is listening on port 7080 (CONFLICT)"
elif ss -tlnp 2>/dev/null | grep ":7080" | grep -q "apache"; then
    print_status 1 "Apache is listening on port 7080 (CONFLICT)"
else
    print_status 0 "No Apache port conflicts"
fi

echo ""
echo "6. Checking MySQL Connection"
echo "============================="

# Check if MySQL is running
if systemctl is-active --quiet mysql 2>/dev/null || systemctl is-active --quiet mariadb 2>/dev/null; then
    print_status 0 "MySQL/MariaDB is running"
else
    print_status 1 "MySQL/MariaDB is NOT running"
fi

echo ""
echo "7. Testing HTTP Access"
echo "======================"

# Test if phpMyAdmin is accessible via HTTP
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:7080/phpmyadmin/ 2>/dev/null || echo "000")
if [ "$HTTP_CODE" = "302" ] || [ "$HTTP_CODE" = "200" ]; then
    print_status 0 "phpMyAdmin is accessible via HTTP (status: $HTTP_CODE)"
elif [ "$HTTP_CODE" = "000" ]; then
    print_status 1 "Could not connect to port 7080"
else
    print_status 1 "phpMyAdmin returned unexpected status: $HTTP_CODE"
fi

# Check if it's being served by Nginx (not Apache)
SERVER_HEADER=$(curl -s -I http://localhost:7080/phpmyadmin/ 2>/dev/null | grep -i "^Server:" || echo "")
if echo "$SERVER_HEADER" | grep -qi "nginx"; then
    print_status 0 "phpMyAdmin is served by Nginx (not Apache)"
elif echo "$SERVER_HEADER" | grep -qi "apache"; then
    print_status 1 "phpMyAdmin is served by Apache (should be Nginx)"
else
    print_warning "Could not determine web server from headers"
fi

echo ""
echo "=================================================="
echo "Verification Summary"
echo "=================================================="

if $ALL_PASSED; then
    echo -e "${GREEN}✓ All checks passed!${NC}"
    echo ""
    echo "phpMyAdmin is correctly configured with Nginx."
    echo "Access it at: http://$(hostname -I | awk '{print $1}'):7080/phpmyadmin/signon"
    echo ""
    echo "Architecture confirmed:"
    echo "  • Web Server: Nginx only (no Apache)"
    echo "  • PHP Processing: PHP-FPM"
    echo "  • Authentication: SSO (Single Sign-On)"
    echo "  • Port: 7080"
else
    echo -e "${RED}✗ Some checks failed${NC}"
    echo ""
    echo "Please review the errors above and:"
    echo "  1. Ensure NovaPanel installation completed successfully"
    echo "  2. Check /var/log/nginx/error.log for errors"
    echo "  3. Run: sudo systemctl status nginx php8.2-fpm mysql"
    echo "  4. See docs/PHPMYADMIN_NGINX_IMPLEMENTATION.md for troubleshooting"
fi

echo ""
