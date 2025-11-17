<?php

return [
    // Panel SQLite database (for NovaPanel operations ONLY)
    // All panel data is stored in SQLite: users, sites, databases, FTP, cron, DNS records
    // This is NOT for customer data - only for panel management
    'panel' => [
        'driver' => 'sqlite',
        'path' => __DIR__ . '/../storage/panel.db',
    ],
    
    // MySQL root credentials (for creating CUSTOMER databases)
    // These credentials are used when panel users request MySQL databases for their sites
    // The panel itself does NOT use MySQL - only SQLite
    'mysql' => [
        'host' => getenv('MYSQL_HOST') ?: 'localhost',
        'root_user' => getenv('MYSQL_ROOT_USER') ?: 'root',
        'root_password' => getenv('MYSQL_ROOT_PASSWORD') ?: '',
    ],
    
    // PostgreSQL root credentials (for creating CUSTOMER databases if using PostgreSQL)
    // These credentials are used when panel users request PostgreSQL databases for their sites
    // The panel itself does NOT use PostgreSQL - only SQLite
    'pgsql' => [
        'host' => getenv('PGSQL_HOST') ?: 'localhost',
        'root_user' => getenv('PGSQL_ROOT_USER') ?: 'postgres',
        'root_password' => getenv('PGSQL_ROOT_PASSWORD') ?: '',
    ],
    
    // PowerDNS database credentials (for DNS management)
    // PowerDNS uses its own database (typically MySQL) for DNS zone storage
    'powerdns' => [
        'host' => getenv('POWERDNS_HOST') ?: 'localhost',
        'database' => getenv('POWERDNS_DATABASE') ?: 'powerdns',
        'username' => getenv('POWERDNS_USER') ?: 'powerdns',
        'password' => getenv('POWERDNS_PASSWORD') ?: '',
    ],
];
