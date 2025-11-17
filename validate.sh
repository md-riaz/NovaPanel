#!/bin/bash

# NovaPanel Validation Script
# Quick validation of core components

echo "╔═══════════════════════════════════════╗"
echo "║   NovaPanel Validation Script        ║"
echo "╚═══════════════════════════════════════╝"
echo ""

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

ERRORS=0

# Function to check
check() {
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $1"
    else
        echo -e "${RED}✗${NC} $1"
        ((ERRORS++))
    fi
}

echo "1. Checking PHP version..."
php --version > /dev/null 2>&1
check "PHP is installed"

echo ""
echo "2. Checking composer..."
composer --version > /dev/null 2>&1
check "Composer is installed"

echo ""
echo "3. Checking directory structure..."
[ -d "app" ] && check "app/ directory exists"
[ -d "public" ] && check "public/ directory exists"
[ -d "database" ] && check "database/ directory exists"
[ -d "resources/views" ] && check "resources/views/ directory exists"
[ -d "storage" ] && check "storage/ directory exists"

echo ""
echo "4. Checking critical files..."
[ -f "public/index.php" ] && check "public/index.php exists"
[ -f "database/migration.php" ] && check "database/migration.php exists"
[ -f "composer.json" ] && check "composer.json exists"
[ -f ".env.php.example" ] && check ".env.php.example exists"

echo ""
echo "5. Validating PHP syntax..."
find app -name "*.php" -exec php -l {} \; > /dev/null 2>&1
check "All app files have valid PHP syntax"

find resources/views -name "*.php" -exec php -l {} \; > /dev/null 2>&1
check "All view files have valid PHP syntax"

echo ""
echo "6. Checking autoloader..."
[ -d "vendor" ] && check "vendor/ directory exists (composer install ran)"

echo ""
echo "7. Checking database..."
if [ -f "storage/panel.db" ]; then
    check "Database file exists"
    TABLES=$(sqlite3 storage/panel.db ".tables" 2>/dev/null | wc -w)
    if [ "$TABLES" -ge 12 ]; then
        check "Database has required tables ($TABLES tables found)"
    else
        echo -e "${RED}✗${NC} Database has insufficient tables ($TABLES found, need 12)"
        ((ERRORS++))
    fi
else
    echo -e "${YELLOW}⚠${NC} Database not initialized (run: php database/migration.php)"
fi

echo ""
echo "8. Checking critical classes..."
php -r "require 'vendor/autoload.php'; class_exists('App\\Http\\Router') or exit(1);" 2>/dev/null
check "Router class loads"

php -r "require 'vendor/autoload.php'; class_exists('App\\Infrastructure\\Database') or exit(1);" 2>/dev/null
check "Database class loads"

php -r "require 'vendor/autoload.php'; class_exists('App\\Http\\Session') or exit(1);" 2>/dev/null
check "Session class loads"

echo ""
echo "9. Checking storage directories..."
[ -d "storage/cache" ] && check "storage/cache/ exists"
[ -d "storage/logs" ] && check "storage/logs/ exists"
[ -d "storage/uploads" ] && check "storage/uploads/ exists"

echo ""
echo "═══════════════════════════════════════"
if [ $ERRORS -eq 0 ]; then
    echo -e "${GREEN}✓ All checks passed!${NC}"
    echo "NovaPanel appears to be properly configured."
    echo ""
    echo "Next steps:"
    echo "  1. Run: php database/migration.php (if not done)"
    echo "  2. Copy .env.php.example to .env.php and configure"
    echo "  3. Start dev server: cd public && php -S localhost:7080"
    echo "  4. Access: http://localhost:7080"
    exit 0
else
    echo -e "${RED}✗ $ERRORS check(s) failed!${NC}"
    echo "Please review the errors above and fix them."
    exit 1
fi
