<?php

require_once __DIR__ . '/../vendor/autoload.php';

use App\Http\Router;
use App\Http\Request;
use App\Http\Controllers\DashboardController;
use App\Http\Controllers\SiteController;
use App\Http\Controllers\UserController;
use App\Http\Controllers\TerminalController;
use App\Http\Controllers\DatabaseController;
use App\Http\Controllers\FtpController;
use App\Http\Controllers\CronController;
use App\Http\Controllers\DnsController;

$router = new Router();

// Dashboard routes
$router->get('/', DashboardController::class . '@index');
$router->get('/dashboard', DashboardController::class . '@index');
$router->get('/dashboard/stats', DashboardController::class . '@stats');

// User routes
$router->get('/users', UserController::class . '@index');
$router->get('/users/create', UserController::class . '@create');
$router->post('/users', UserController::class . '@store');
$router->get('/users/{id}/edit', UserController::class . '@edit');
$router->post('/users/{id}', UserController::class . '@update');
$router->post('/users/{id}/delete', UserController::class . '@delete');

// Site routes
$router->get('/sites', SiteController::class . '@index');
$router->get('/sites/create', SiteController::class . '@create');
$router->post('/sites', SiteController::class . '@store');

// Database routes
$router->get('/databases', DatabaseController::class . '@index');
$router->get('/databases/create', DatabaseController::class . '@create');
$router->post('/databases', DatabaseController::class . '@store');
$router->post('/databases/{id}/delete', DatabaseController::class . '@delete');

// FTP routes
$router->get('/ftp', FtpController::class . '@index');
$router->get('/ftp/create', FtpController::class . '@create');
$router->post('/ftp', FtpController::class . '@store');
$router->post('/ftp/{id}/delete', FtpController::class . '@delete');

// Cron routes
$router->get('/cron', CronController::class . '@index');
$router->get('/cron/create', CronController::class . '@create');
$router->post('/cron', CronController::class . '@store');
$router->post('/cron/{id}/delete', CronController::class . '@delete');

// DNS routes
$router->get('/dns', DnsController::class . '@index');
$router->get('/dns/create', DnsController::class . '@create');
$router->post('/dns', DnsController::class . '@store');
$router->get('/dns/{id}', DnsController::class . '@show');
$router->post('/dns/{id}/records', DnsController::class . '@addRecord');
$router->post('/dns/{domainId}/records/{recordId}/delete', DnsController::class . '@deleteRecord');

// Terminal routes
$router->get('/terminal', TerminalController::class . '@index');
$router->post('/terminal/start', TerminalController::class . '@start');
$router->post('/terminal/stop', TerminalController::class . '@stop');
$router->post('/terminal/restart', TerminalController::class . '@restart');
$router->get('/terminal/status', TerminalController::class . '@status');

// Dispatch request
$request = new Request();
$response = $router->dispatch($request);
$response->send();
