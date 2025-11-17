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
    
    // BIND9 configuration (for DNS management)
    // BIND9 uses zone files for complete isolation from databases
    'bind9' => [
        'zones_path' => getenv('BIND9_ZONES_PATH') ?: '/etc/bind/zones',
        'named_conf_path' => getenv('BIND9_NAMED_CONF_PATH') ?: '/etc/bind/named.conf.local',
    ],
];
