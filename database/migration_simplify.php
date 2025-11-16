<?php

require_once __DIR__ . '/../vendor/autoload.php';

use App\Infrastructure\Database;

echo "NovaPanel Database Schema Update - Simplify to Single VPS Model\n";
echo "================================================================\n\n";

try {
    $db = Database::panel();
    $db->setAttribute(PDO::ATTR_TIMEOUT, 30);
    $db->exec('PRAGMA journal_mode=WAL');
    
    echo "Starting migration to simplified schema...\n\n";

    // Check if we need to migrate data
    $stmt = $db->query("SELECT COUNT(*) as count FROM sites");
    $siteCount = $stmt->fetch()['count'];
    
    if ($siteCount > 0) {
        echo "⚠ Warning: Found $siteCount existing site(s). These will need to be migrated.\n";
        echo "Migrating sites to link directly to users...\n";
        
        // For each site, link it to the user who owns the account
        $db->exec("
            CREATE TABLE IF NOT EXISTS sites_new (
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
        
        // Migrate data: site.account_id -> account.user_id -> site.user_id
        $db->exec("
            INSERT INTO sites_new (id, user_id, domain, document_root, php_version, ssl_enabled, created_at, updated_at)
            SELECT s.id, a.user_id, s.domain, s.document_root, s.php_version, s.ssl_enabled, s.created_at, s.updated_at
            FROM sites s
            INNER JOIN accounts a ON s.account_id = a.id
        ");
        
        // Drop old sites table and rename new one
        $db->exec("DROP TABLE sites");
        $db->exec("ALTER TABLE sites_new RENAME TO sites");
        
        echo "✓ Migrated $siteCount site(s) to new schema\n";
    } else {
        // No existing sites, just update the schema
        echo "No existing sites found. Updating schema...\n";
        
        $db->exec("DROP TABLE IF EXISTS sites");
        
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
        echo "✓ Created new sites table with user_id reference\n";
    }
    
    // Update domains table to reference sites correctly
    echo "\nUpdating domains table...\n";
    $db->exec("
        CREATE TABLE IF NOT EXISTS domains_new (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            site_id INTEGER NOT NULL,
            name TEXT UNIQUE NOT NULL,
            created_at TEXT NOT NULL DEFAULT (datetime('now')),
            FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE
        )
    ");
    
    // Check if domains table exists and has data
    $stmt = $db->query("SELECT COUNT(*) as count FROM sqlite_master WHERE type='table' AND name='domains'");
    if ($stmt->fetch()['count'] > 0) {
        $stmt = $db->query("SELECT COUNT(*) as count FROM domains");
        $domainCount = $stmt->fetch()['count'];
        
        if ($domainCount > 0) {
            $db->exec("INSERT INTO domains_new SELECT * FROM domains");
            echo "✓ Migrated $domainCount domain(s)\n";
        }
        
        $db->exec("DROP TABLE domains");
    }
    
    $db->exec("ALTER TABLE domains_new RENAME TO domains");
    echo "✓ Domains table updated\n";
    
    // Update FTP users to reference users instead of accounts
    echo "\nUpdating ftp_users table...\n";
    
    $stmt = $db->query("SELECT COUNT(*) as count FROM ftp_users");
    $ftpCount = $stmt->fetch()['count'];
    
    if ($ftpCount > 0) {
        echo "Found $ftpCount FTP user(s). Migrating...\n";
        
        $db->exec("
            CREATE TABLE ftp_users_new (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                username TEXT UNIQUE NOT NULL,
                home_directory TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 1,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )
        ");
        
        $db->exec("
            INSERT INTO ftp_users_new (id, user_id, username, home_directory, enabled, created_at)
            SELECT f.id, a.user_id, f.username, f.home_directory, f.enabled, f.created_at
            FROM ftp_users f
            INNER JOIN accounts a ON f.account_id = a.id
        ");
        
        $db->exec("DROP TABLE ftp_users");
        $db->exec("ALTER TABLE ftp_users_new RENAME TO ftp_users");
        echo "✓ Migrated $ftpCount FTP user(s)\n";
    } else {
        $db->exec("DROP TABLE IF EXISTS ftp_users");
        $db->exec("
            CREATE TABLE ftp_users (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                username TEXT UNIQUE NOT NULL,
                home_directory TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 1,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )
        ");
        echo "✓ Created new ftp_users table\n";
    }
    
    // Update cron_jobs table
    echo "\nUpdating cron_jobs table...\n";
    
    $stmt = $db->query("SELECT COUNT(*) as count FROM cron_jobs");
    $cronCount = $stmt->fetch()['count'];
    
    if ($cronCount > 0) {
        echo "Found $cronCount cron job(s). Migrating...\n";
        
        $db->exec("
            CREATE TABLE cron_jobs_new (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                schedule TEXT NOT NULL,
                command TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 1,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )
        ");
        
        $db->exec("
            INSERT INTO cron_jobs_new (id, user_id, schedule, command, enabled, created_at)
            SELECT c.id, a.user_id, c.schedule, c.command, c.enabled, c.created_at
            FROM cron_jobs c
            INNER JOIN accounts a ON c.account_id = a.id
        ");
        
        $db->exec("DROP TABLE cron_jobs");
        $db->exec("ALTER TABLE cron_jobs_new RENAME TO cron_jobs");
        echo "✓ Migrated $cronCount cron job(s)\n";
    } else {
        $db->exec("DROP TABLE IF EXISTS cron_jobs");
        $db->exec("
            CREATE TABLE cron_jobs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                schedule TEXT NOT NULL,
                command TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 1,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )
        ");
        echo "✓ Created new cron_jobs table\n";
    }
    
    // Update databases table
    echo "\nUpdating databases table...\n";
    
    $stmt = $db->query("SELECT COUNT(*) as count FROM databases");
    $dbCount = $stmt->fetch()['count'];
    
    if ($dbCount > 0) {
        echo "Found $dbCount database(s). Migrating...\n";
        
        $db->exec("
            CREATE TABLE databases_new (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                name TEXT UNIQUE NOT NULL,
                type TEXT NOT NULL DEFAULT 'mysql',
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )
        ");
        
        $db->exec("
            INSERT INTO databases_new (id, user_id, name, type, created_at)
            SELECT d.id, a.user_id, d.name, d.type, d.created_at
            FROM databases d
            INNER JOIN accounts a ON d.account_id = a.id
        ");
        
        $db->exec("DROP TABLE databases");
        $db->exec("ALTER TABLE databases_new RENAME TO databases");
        echo "✓ Migrated $dbCount database(s)\n";
    } else {
        $db->exec("DROP TABLE IF EXISTS databases");
        $db->exec("
            CREATE TABLE databases (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                name TEXT UNIQUE NOT NULL,
                type TEXT NOT NULL DEFAULT 'mysql',
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )
        ");
        echo "✓ Created new databases table\n";
    }
    
    echo "\n✅ Migration completed successfully!\n";
    echo "\nSummary:\n";
    echo "- Sites now link directly to panel users (user_id)\n";
    echo "- FTP users now link directly to panel users\n";
    echo "- Cron jobs now link directly to panel users\n";
    echo "- Databases now link directly to panel users\n";
    echo "- Accounts table is deprecated (can be removed in future)\n";
    echo "\nAll resources are now managed at the panel user level.\n";
    
} catch (Exception $e) {
    echo "\n❌ Error: " . $e->getMessage() . "\n";
    echo "Stack trace:\n" . $e->getTraceAsString() . "\n";
    exit(1);
}
