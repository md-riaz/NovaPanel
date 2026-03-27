<?php

require_once __DIR__ . '/../vendor/autoload.php';

use App\Infrastructure\Database;

$rolePermissions = [
    'Admin' => ['*'],
    'AccountOwner' => [
        'users.view', 'users.edit',
        'sites.view', 'sites.create', 'sites.edit', 'sites.delete', 'sites.manage',
        'databases.view', 'databases.create', 'databases.edit', 'databases.delete', 'databases.manage',
        'dns.view', 'dns.create', 'dns.edit', 'dns.delete', 'dns.manage',
        'ftp.view', 'ftp.create', 'ftp.edit', 'ftp.delete', 'ftp.manage',
        'cron.view', 'cron.create', 'cron.edit', 'cron.delete', 'cron.manage',
        'terminal.access',
    ],
    'Developer' => [
        'users.view', 'users.edit',
        'sites.view', 'sites.create', 'sites.edit',
        'databases.view', 'databases.create', 'databases.edit',
        'dns.view',
        'ftp.view', 'ftp.create',
        'cron.view', 'cron.create', 'cron.edit',
        'terminal.access',
    ],
    'ReadOnly' => [
        'users.view', 'users.edit',
        'sites.view',
        'databases.view',
        'dns.view',
        'ftp.view',
        'cron.view',
    ],
];
function ensureColumn(\PDO $db, string $table, string $column, string $definition): void
{
    $quotedTable = '"' . str_replace('"', '""', $table) . '"';
    $quotedColumn = '"' . str_replace('"', '""', $column) . '"';

    $columns = $db->query("PRAGMA table_info({$quotedTable})")->fetchAll(\PDO::FETCH_ASSOC);
    foreach ($columns as $existingColumn) {
        if (($existingColumn['name'] ?? null) === $column) {
            return;
        }
    }

    $db->exec(sprintf('ALTER TABLE %s ADD COLUMN %s %s', $quotedTable, $quotedColumn, $definition));
    echo "✓ Ensured {$table}.{$column} column\n";
}

echo "NovaPanel Database Migration\n";
echo "=============================\n\n";

try {
    $db = Database::panel();

    echo "Creating tables...\n";

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

    $db->exec("
        CREATE TABLE IF NOT EXISTS roles (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            description TEXT
        )
    ");
    echo "✓ Created roles table\n";

    $db->exec("
        CREATE TABLE IF NOT EXISTS permissions (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            description TEXT
        )
    ");
    echo "✓ Created permissions table\n";

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

    $db->exec("
        CREATE TABLE IF NOT EXISTS sites (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id INTEGER NOT NULL,
            domain TEXT UNIQUE NOT NULL,
            document_root TEXT NOT NULL,
            php_version TEXT NOT NULL DEFAULT '8.2',
            ssl_enabled INTEGER NOT NULL DEFAULT 0,
            certificate_provider TEXT DEFAULT 'letsencrypt',
            certificate_status TEXT NOT NULL DEFAULT 'unissued',
            certificate_expires_at TEXT,
            certificate_auto_renew INTEGER NOT NULL DEFAULT 1,
            certificate_validation_method TEXT NOT NULL DEFAULT 'webroot',
            certificate_path TEXT,
            certificate_key_path TEXT,
            force_https INTEGER NOT NULL DEFAULT 0,
            last_certificate_renewal_at TEXT,
            last_certificate_error TEXT,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            updated_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created sites table\n";

    ensureColumn($db, 'sites', 'certificate_provider', "TEXT DEFAULT 'letsencrypt'");
    ensureColumn($db, 'sites', 'certificate_status', "TEXT NOT NULL DEFAULT 'unissued'");
    ensureColumn($db, 'sites', 'certificate_expires_at', 'TEXT');
    ensureColumn($db, 'sites', 'certificate_auto_renew', 'INTEGER NOT NULL DEFAULT 1');
    ensureColumn($db, 'sites', 'certificate_validation_method', "TEXT NOT NULL DEFAULT 'webroot'");
    ensureColumn($db, 'sites', 'certificate_path', 'TEXT');
    ensureColumn($db, 'sites', 'certificate_key_path', 'TEXT');
    ensureColumn($db, 'sites', 'force_https', 'INTEGER NOT NULL DEFAULT 0');
    ensureColumn($db, 'sites', 'last_certificate_renewal_at', 'TEXT');
    ensureColumn($db, 'sites', 'last_certificate_error', 'TEXT');

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

    $stmt = $db->prepare('INSERT OR IGNORE INTO roles (name, description) VALUES (?, ?)');
    $roles = [
        ['Admin', 'Full system administrator access'],
        ['AccountOwner', 'Can manage only their own sites, databases, DNS, FTP users, and cron jobs'],
        ['Developer', 'Can work with their own sites, databases, FTP users, and cron jobs without broader account management'],
        ['ReadOnly', 'Can only view their own resources'],
    ];

    foreach ($roles as $role) {
        $stmt->execute($role);
    }
    echo "✓ Inserted default roles\n";

    $db->exec("
        CREATE TABLE IF NOT EXISTS terminal_sessions (
            id TEXT PRIMARY KEY,
            user_id INTEGER NOT NULL,
            role TEXT NOT NULL,
            ttyd_port INTEGER,
            process_id INTEGER,
            status TEXT NOT NULL DEFAULT 'pending',
            expires_at TEXT NOT NULL,
            last_seen_at TEXT NOT NULL,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        )
    ");
    echo "✓ Created terminal_sessions table\n";

    $stmt = $db->prepare('INSERT OR IGNORE INTO permissions (name, description) VALUES (?, ?)');
    $permissions = [
        ['users.view', 'View panel users'],
        ['users.create', 'Create panel users'],
        ['users.edit', 'Edit panel users'],
        ['users.delete', 'Delete panel users'],
        ['users.manage', 'Manage panel users and role assignments'],
        ['sites.view', 'View sites'],
        ['sites.create', 'Create sites'],
        ['sites.edit', 'Edit sites'],
        ['sites.delete', 'Delete sites'],
        ['sites.manage', 'Manage any site within the panel'],
        ['databases.view', 'View databases'],
        ['databases.create', 'Create databases'],
        ['databases.edit', 'Edit databases'],
        ['databases.delete', 'Delete databases'],
        ['databases.manage', 'Manage any database within the panel'],
        ['dns.view', 'View DNS zones and records'],
        ['dns.create', 'Create DNS zones'],
        ['dns.edit', 'Edit DNS records'],
        ['dns.delete', 'Delete DNS records'],
        ['dns.manage', 'Manage DNS zones and records for any account'],
        ['ftp.view', 'View FTP users'],
        ['ftp.create', 'Create FTP users'],
        ['ftp.edit', 'Edit FTP users'],
        ['ftp.delete', 'Delete FTP users'],
        ['ftp.manage', 'Manage FTP users for any account'],
        ['cron.view', 'View cron jobs'],
        ['cron.create', 'Create cron jobs'],
        ['cron.edit', 'Edit cron jobs'],
        ['cron.delete', 'Delete cron jobs'],
        ['cron.manage', 'Manage cron jobs for any account'],
        ['terminal.access', 'Access the web terminal'],
        ['system.settings', 'Manage system settings'],
        ['system.logs', 'View system logs'],
    ];

    foreach ($permissions as $permission) {
        $stmt->execute($permission);
    }
    echo "✓ Inserted default permissions\n";

    $db->exec("
        INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
        SELECT r.id, p.id
        FROM roles r
        CROSS JOIN permissions p
        WHERE r.name = 'Admin'
    ");
    echo "✓ Assigned all permissions to Admin role\n";

    foreach ($rolePermissions as $roleName => $permissionNames) {
        if ($roleName === 'Admin') {
            continue;
        }

        foreach ($permissionNames as $permName) {
            $assignment = $db->prepare("
                INSERT OR IGNORE INTO role_permissions (role_id, permission_id)
                SELECT r.id, p.id
                FROM roles r
                INNER JOIN permissions p ON p.name = ?
                WHERE r.name = ?
            ");
            $assignment->execute([$permName, $roleName]);
        }
    }

    echo "✓ Assigned permissions to non-admin roles\n";
    echo "\n✅ Migration completed successfully!\n";
} catch (Exception $e) {
    echo "\n❌ Error: " . $e->getMessage() . "\n";
    exit(1);
}
