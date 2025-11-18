<?php
/**
 * phpMyAdmin Single Sign-On Script for NovaPanel
 * 
 * This script enables automatic login to phpMyAdmin by reading
 * MySQL credentials from the NovaPanel environment configuration.
 */

// Start session for NovaPanel authentication
session_name('novapanel_session');
session_start();

// Check if user is authenticated in NovaPanel
if (!isset($_SESSION['user_id'])) {
    // Redirect to NovaPanel login page
    header('Location: /login');
    exit;
}

// Load NovaPanel environment configuration
$envFile = __DIR__ . '/../.env.php';
if (file_exists($envFile)) {
    require_once $envFile;
}

// Get MySQL credentials from environment
$mysqlHost = getenv('MYSQL_HOST') ?: 'localhost';
$mysqlUser = getenv('MYSQL_ROOT_USER') ?: 'root';
$mysqlPassword = getenv('MYSQL_ROOT_PASSWORD') ?: '';

// Set phpMyAdmin signon session with MySQL credentials
$_SESSION['novapanel_pma_signon'] = [
    'user' => $mysqlUser,
    'password' => $mysqlPassword,
    'host' => $mysqlHost,
];

// Build redirect URL with optional database parameter
$redirectUrl = '/phpmyadmin/';
if (isset($_GET['db']) && !empty($_GET['db'])) {
    $redirectUrl .= '?db=' . urlencode($_GET['db']);
}

// Redirect to phpMyAdmin
header('Location: ' . $redirectUrl);
exit;
