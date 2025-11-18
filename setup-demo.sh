#!/bin/bash
# Demo setup script (runs without sudo)

echo "Setting up NovaPanel Demo Environment..."

# Create directories
mkdir -p storage public/phpmyadmin

# Create .env.php
cat > .env.php << 'ENVEOF'
<?php
putenv('MYSQL_HOST=localhost');
putenv('MYSQL_ROOT_USER=novapanel_db');
putenv('MYSQL_ROOT_PASSWORD=demo_password_123');
putenv('APP_ENV=development');
putenv('APP_DEBUG=true');
putenv('APP_URL=http://localhost:8000');
ENVEOF

# Create minimal database
sqlite3 storage/panel.db << 'SQLEOF'
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    username TEXT,
    email TEXT,
    password TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO users VALUES (1, 'admin', 'admin@novapanel.local', '$2y$10$test', datetime('now'));

CREATE TABLE databases (
    id INTEGER PRIMARY KEY,
    user_id INTEGER,
    name TEXT,
    type TEXT DEFAULT 'mysql',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO databases VALUES (1, 1, 'wordpress_db', 'mysql', datetime('now'));
INSERT INTO databases VALUES (2, 1, 'laravel_db', 'mysql', datetime('now'));
INSERT INTO databases VALUES (3, 1, 'ecommerce_db', 'mysql', datetime('now'));
SQLEOF

echo "✓ Demo environment created"
echo "  - .env.php configured"
echo "  - Database created with sample data"
echo "  - Ready to test SSO"
