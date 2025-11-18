<?php
/**
 * Database Access Portal - Adminer Integration
 * 
 * This file provides secure access to Adminer for managing MySQL databases
 * through the NovaPanel interface. It requires authentication through NovaPanel.
 */

// Bootstrap NovaPanel (one level up from public)
require_once __DIR__ . '/../app/Http/Session.php';

use App\Http\Session;

// Start session and check authentication
try {
    Session::start();
} catch (\RuntimeException $e) {
    // Session validation failed
    Session::destroy();
    header('Location: /login');
    exit;
}

// Check if user is authenticated in NovaPanel
if (!Session::has('user_id') || Session::get('user_id') === null) {
    header('Location: /login');
    exit;
}

// Load configuration
$configPath = __DIR__ . '/../.env.php';
if (file_exists($configPath)) {
    require_once $configPath;
}

// Get MySQL credentials from environment
$mysqlHost = getenv('MYSQL_HOST') ?: 'localhost';
$mysqlUser = getenv('MYSQL_ROOT_USER') ?: 'root';
$mysqlPassword = getenv('MYSQL_ROOT_PASSWORD') ?: '';

// Selected database from URL parameter (sanitized)
$selectedDb = null;
if (isset($_GET['db']) && !empty($_GET['db'])) {
    // Sanitize database name - alphanumeric, underscore, and dash only
    if (preg_match('/^[a-zA-Z0-9_-]+$/', $_GET['db'])) {
        $selectedDb = $_GET['db'];
    }
}

// Auto-login for Adminer using credentials
// Set server and credentials so Adminer auto-fills them
$_GET['username'] = $mysqlUser;
$_POST['auth'] = array(
    'driver' => 'server',
    'server' => $mysqlHost,
    'username' => $mysqlUser,
    'password' => $mysqlPassword,
    'db' => $selectedDb
);

// Include Adminer
include __DIR__ . '/adminer.php';
