<?php

require_once __DIR__ . '/../vendor/autoload.php';

use App\Infrastructure\Database;

echo "NovaPanel Database Migration\n";
echo "=============================\n\n";

try {
    $db = Database::panel();
    
    echo "Creating tables...\n";

    // Users table
    $db->exec("
        CREATE TABLE IF NOT EXISTS users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            username TEXT UNIQUE NOT NULL,
            email TEXT UNIQUE NOT NULL,
            password TEXT NOT NULL,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            updated_at TEXT NOT NULL DEFAULT (datetime('now'))
        )
    ");
    echo "✓ Created users table\n";

    // Roles table
    $db->exec("
        CREATE TABLE IF NOT EXISTS roles (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            description TEXT
        )
    ");
    echo "✓ Created roles table\n";

    // Permissions table
    $db->exec("
        CREATE TABLE IF NOT EXISTS permissions (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            description TEXT
        )
    ");
    echo "✓ Created permissions table\n";

    // User roles junction table
    $db->exec("
        CREATE TABLE IF NOT EXISTS user_roles (
            user_id INTEGER NOT NULL,
            role_id INTEGER NOT NULL,
            PRIMARY KEY (user_id, role_id),
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
            FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created user_roles table\n";

    // Role permissions junction table
    $db->exec("
        CREATE TABLE IF NOT EXISTS role_permissions (
            role_id INTEGER NOT NULL,
            permission_id INTEGER NOT NULL,
            PRIMARY KEY (role_id, permission_id),
            FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE,
            FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created role_permissions table\n";

    // Sites table (linked directly to users for single VPS)
    $db->exec("
        CREATE TABLE IF NOT EXISTS sites (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id INTEGER NOT NULL,
            domain TEXT UNIQUE NOT NULL,
            document_root TEXT NOT NULL,
            php_version TEXT NOT NULL DEFAULT '8.2',
            ssl_enabled INTEGER NOT NULL DEFAULT 0,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            updated_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created sites table\n";

    // Domains table
    $db->exec("
        CREATE TABLE IF NOT EXISTS domains (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            site_id INTEGER NOT NULL,
            name TEXT UNIQUE NOT NULL,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created domains table\n";

    // DNS records table
    $db->exec("
        CREATE TABLE IF NOT EXISTS dns_records (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            domain_id INTEGER NOT NULL,
            name TEXT NOT NULL,
            type TEXT NOT NULL,
            content TEXT NOT NULL,
            ttl INTEGER NOT NULL DEFAULT 3600,
            priority INTEGER,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (domain_id) REFERENCES domains(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created dns_records table\n";

    // FTP users table (linked directly to users)
    $db->exec("
        CREATE TABLE IF NOT EXISTS ftp_users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id INTEGER NOT NULL,
            username TEXT UNIQUE NOT NULL,
            home_directory TEXT NOT NULL,
            enabled INTEGER NOT NULL DEFAULT 1,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created ftp_users table\n";

    // Cron jobs table (linked directly to users)
    $db->exec("
        CREATE TABLE IF NOT EXISTS cron_jobs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id INTEGER NOT NULL,
            schedule TEXT NOT NULL,
            command TEXT NOT NULL,
            enabled INTEGER NOT NULL DEFAULT 1,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created cron_jobs table\n";

    // Databases table (linked directly to users)
    $db->exec("
        CREATE TABLE IF NOT EXISTS databases (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id INTEGER NOT NULL,
            name TEXT UNIQUE NOT NULL,
            type TEXT NOT NULL DEFAULT 'mysql',
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created databases table\n";

    // Database users table
    $db->exec("
        CREATE TABLE IF NOT EXISTS database_users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            database_id INTEGER NOT NULL,
            username TEXT NOT NULL,
            host TEXT NOT NULL DEFAULT 'localhost',
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (database_id) REFERENCES databases(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created database_users table\n";

    // Insert default roles
    $stmt = $db->prepare("INSERT OR IGNORE INTO roles (name, description) VALUES (?, ?)");
    $roles = [
        ['Admin', 'Full system administrator access'],
        ['AccountOwner', 'Account owner with full control over their account'],
        ['Developer', 'Developer with limited access to sites and databases'],
        ['ReadOnly', 'Read-only access to view information']
    ];
    
    foreach ($roles as $role) {
        $stmt->execute($role);
    }
    echo "✓ Inserted default roles\n";

    // Insert default permissions
    $stmt = $db->prepare("INSERT OR IGNORE INTO permissions (name, description) VALUES (?, ?)");
    $permissions = [
        ['accounts.view', 'View accounts'],
        ['accounts.create', 'Create accounts'],
        ['accounts.edit', 'Edit accounts'],
        ['accounts.delete', 'Delete accounts'],
        ['sites.view', 'View sites'],
        ['sites.create', 'Create sites'],
        ['sites.edit', 'Edit sites'],
        ['sites.delete', 'Delete sites'],
        ['databases.view', 'View databases'],
        ['databases.create', 'Create databases'],
        ['databases.delete', 'Delete databases'],
        ['dns.view', 'View DNS records'],
        ['dns.edit', 'Edit DNS records'],
        ['ftp.view', 'View FTP users'],
        ['ftp.create', 'Create FTP users'],
        ['ftp.delete', 'Delete FTP users'],
        ['cron.view', 'View cron jobs'],
        ['cron.create', 'Create cron jobs'],
        ['cron.edit', 'Edit cron jobs'],
        ['cron.delete', 'Delete cron jobs']
    ];
    
    foreach ($permissions as $permission) {
        $stmt->execute($permission);
    }
    echo "✓ Inserted default permissions\n";

    // Assign all permissions to Admin role
    $db->exec("
        INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
        SELECT r.id, p.id
        FROM roles r
        CROSS JOIN permissions p
        WHERE r.name = 'Admin'
    ");
    echo "✓ Assigned all permissions to Admin role\n";

    echo "\n✅ Migration completed successfully!\n";
    
} catch (Exception $e) {
    echo "\n❌ Error: " . $e->getMessage() . "\n";
    exit(1);
}
