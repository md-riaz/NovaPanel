<?php

return [
    // Panel SQLite database
    'panel' => [
        'driver' => 'sqlite',
        'path' => __DIR__ . '/../storage/panel.db',
    ],
    
    // MySQL root credentials for creating user databases
    'mysql' => [
        'host' => getenv('MYSQL_HOST') ?: 'localhost',
        'root_user' => getenv('MYSQL_ROOT_USER') ?: 'root',
        'root_password' => getenv('MYSQL_ROOT_PASSWORD') ?: '',
    ],
    
    // PostgreSQL root credentials (if using PostgreSQL)
    'pgsql' => [
        'host' => getenv('PGSQL_HOST') ?: 'localhost',
        'root_user' => getenv('PGSQL_ROOT_USER') ?: 'postgres',
        'root_password' => getenv('PGSQL_ROOT_PASSWORD') ?: '',
    ],
    
    // PowerDNS database credentials
    'powerdns' => [
        'host' => getenv('POWERDNS_HOST') ?: 'localhost',
        'database' => getenv('POWERDNS_DATABASE') ?: 'powerdns',
        'username' => getenv('POWERDNS_USER') ?: 'powerdns',
        'password' => getenv('POWERDNS_PASSWORD') ?: '',
    ],
];
