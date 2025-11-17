<?php

// Load environment configuration
$envFile = __DIR__ . '/../.env.php';
if (file_exists($envFile)) {
    require_once $envFile;
}

require_once __DIR__ . '/../vendor/autoload.php';

use App\Http\Router;
use App\Http\Request;
use App\Http\Controllers\AuthController;
use App\Http\Controllers\DashboardController;
use App\Http\Controllers\SiteController;
use App\Http\Controllers\UserController;
use App\Http\Controllers\TerminalController;
use App\Http\Controllers\DatabaseController;
use App\Http\Controllers\FtpController;
use App\Http\Controllers\CronController;
use App\Http\Controllers\DnsController;
use App\Http\Middleware\AuthMiddleware;

$router = new Router();

// Authentication routes (no auth middleware)
$router->get('/login', AuthController::class . '@showLogin');
$router->post('/login', AuthController::class . '@login');
$router->post('/logout', AuthController::class . '@logout');
$router->get('/logout', AuthController::class . '@logout');

// Protected routes (require authentication)
// Dashboard routes
$router->get('/', DashboardController::class . '@index', [AuthMiddleware::class]);
$router->get('/dashboard', DashboardController::class . '@index', [AuthMiddleware::class]);
$router->get('/dashboard/stats', DashboardController::class . '@stats', [AuthMiddleware::class]);

// User routes
$router->get('/users', UserController::class . '@index', [AuthMiddleware::class]);
$router->get('/users/create', UserController::class . '@create', [AuthMiddleware::class]);
$router->post('/users', UserController::class . '@store', [AuthMiddleware::class]);
$router->get('/users/{id}/edit', UserController::class . '@edit', [AuthMiddleware::class]);
$router->post('/users/{id}', UserController::class . '@update', [AuthMiddleware::class]);
$router->post('/users/{id}/delete', UserController::class . '@delete', [AuthMiddleware::class]);

// Site routes
$router->get('/sites', SiteController::class . '@index', [AuthMiddleware::class]);
$router->get('/sites/create', SiteController::class . '@create', [AuthMiddleware::class]);
$router->post('/sites', SiteController::class . '@store', [AuthMiddleware::class]);

// Database routes
$router->get('/databases', DatabaseController::class . '@index', [AuthMiddleware::class]);
$router->get('/databases/create', DatabaseController::class . '@create', [AuthMiddleware::class]);
$router->post('/databases', DatabaseController::class . '@store', [AuthMiddleware::class]);
$router->post('/databases/{id}/delete', DatabaseController::class . '@delete', [AuthMiddleware::class]);

// FTP routes
$router->get('/ftp', FtpController::class . '@index', [AuthMiddleware::class]);
$router->get('/ftp/create', FtpController::class . '@create', [AuthMiddleware::class]);
$router->post('/ftp', FtpController::class . '@store', [AuthMiddleware::class]);
$router->post('/ftp/{id}/delete', FtpController::class . '@delete', [AuthMiddleware::class]);

// Cron routes
$router->get('/cron', CronController::class . '@index', [AuthMiddleware::class]);
$router->get('/cron/create', CronController::class . '@create', [AuthMiddleware::class]);
$router->post('/cron', CronController::class . '@store', [AuthMiddleware::class]);
$router->post('/cron/{id}/delete', CronController::class . '@delete', [AuthMiddleware::class]);

// DNS routes
$router->get('/dns', DnsController::class . '@index', [AuthMiddleware::class]);
$router->get('/dns/create', DnsController::class . '@create', [AuthMiddleware::class]);
$router->post('/dns', DnsController::class . '@store', [AuthMiddleware::class]);
$router->get('/dns/{id}', DnsController::class . '@show', [AuthMiddleware::class]);
$router->post('/dns/{id}/records', DnsController::class . '@addRecord', [AuthMiddleware::class]);
$router->post('/dns/{domainId}/records/{recordId}/delete', DnsController::class . '@deleteRecord', [AuthMiddleware::class]);

// Terminal routes
$router->get('/terminal', TerminalController::class . '@index', [AuthMiddleware::class]);
$router->post('/terminal/start', TerminalController::class . '@start', [AuthMiddleware::class]);
$router->post('/terminal/stop', TerminalController::class . '@stop', [AuthMiddleware::class]);
$router->post('/terminal/restart', TerminalController::class . '@restart', [AuthMiddleware::class]);
$router->get('/terminal/status', TerminalController::class . '@status', [AuthMiddleware::class]);

// Dispatch request
$request = new Request();
$response = $router->dispatch($request);
$response->send();
