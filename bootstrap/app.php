<?php

/**
 * NovaPanel Bootstrap File
 * 
 * This file is responsible for initializing the application:
 * 1. Loading environment configuration
 * 2. Setting up error reporting
 * 3. Loading composer autoloader
 * 4. Initializing core services
 */

// Load composer autoloader
require_once __DIR__ . '/../vendor/autoload.php';

// Load environment configuration (happens once, centrally)
App\Support\Env::load();

// Set error reporting based on environment
$appEnv = App\Support\Env::get('APP_ENV', 'production');
$appDebug = App\Support\Env::get('APP_DEBUG', 'false') === 'true';

if ($appDebug || $appEnv === 'development') {
    ini_set('display_errors', 1);
    ini_set('display_startup_errors', 1);
    error_reporting(E_ALL);
} else {
    ini_set('display_errors', 0);
    ini_set('display_startup_errors', 0);
    error_reporting(0);
}

// Return app environment info (can be used by index.php if needed)
return [
    'env' => $appEnv,
    'debug' => $appDebug,
];
