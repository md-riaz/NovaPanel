<?php

/**
 * NovaPanel Configuration Example
 * 
 * Copy this file to .env or set environment variables directly
 */

// Application
putenv('APP_ENV=production');
putenv('APP_DEBUG=false');
putenv('APP_URL=http://your-server-ip:7080');

// MySQL Root Credentials (for creating user databases)
putenv('MYSQL_HOST=localhost');
putenv('MYSQL_ROOT_USER=root');
putenv('MYSQL_ROOT_PASSWORD=your_mysql_root_password');

// PostgreSQL Root Credentials (optional, if using PostgreSQL)
// putenv('PGSQL_HOST=localhost');
// putenv('PGSQL_ROOT_USER=postgres');
// putenv('PGSQL_ROOT_PASSWORD=your_pgsql_root_password');

// PowerDNS Database Credentials (for DNS management)
putenv('POWERDNS_HOST=localhost');
putenv('POWERDNS_DATABASE=powerdns');
putenv('POWERDNS_USER=powerdns');
putenv('POWERDNS_PASSWORD=your_powerdns_password');
