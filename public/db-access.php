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

// Auto-login plugin for Adminer
// This allows seamless access without requiring users to enter credentials again
function adminer_object() {
    // Load Adminer plugin class
    include_once __DIR__ . '/adminer.php';
    
    return new AdminerNovaPanel();
}

class AdminerNovaPanel extends Adminer {
    function name() {
        return 'NovaPanel Database Manager';
    }
    
    function credentials() {
        // Auto-login with panel's MySQL credentials
        global $mysqlHost, $mysqlUser, $mysqlPassword;
        return array($mysqlHost, $mysqlUser, $mysqlPassword);
    }
    
    function login($login, $password) {
        // Always return true since we're using auto-login with panel credentials
        return true;
    }
    
    function database() {
        // If a specific database is requested via URL parameter, select it
        if (isset($_GET['db']) && !empty($_GET['db'])) {
            return $_GET['db'];
        }
        // Otherwise allow access to all databases
        return null;
    }
    
    // Customize the appearance
    function css() {
        $css = parent::css();
        return array_merge((array) $css, array('/assets/css/adminer-custom.css'));
    }
    
    function permanentLogin() {
        // Keep user logged in through Adminer sessions
        return true;
    }
}

// Include Adminer
include __DIR__ . '/adminer.php';
