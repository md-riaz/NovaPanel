<?php

/**
 * NovaPanel Entry Point
 *
 * This file handles all HTTP requests to the panel.
 */

require_once __DIR__ . '/../bootstrap/app.php';

use App\Http\Controllers\AuthController;
use App\Http\Controllers\CronController;
use App\Http\Controllers\DashboardController;
use App\Http\Controllers\DatabaseController;
use App\Http\Controllers\DnsController;
use App\Http\Controllers\FtpController;
use App\Http\Controllers\SiteController;
use App\Http\Controllers\TerminalController;
use App\Http\Controllers\UserController;
use App\Http\Middleware\AuthMiddleware;
use App\Http\Middleware\PermissionMiddleware;
use App\Http\Request;
use App\Http\Router;

$router = new Router();
$auth = [AuthMiddleware::class];
$withPermission = static fn (string ...$permissions): array => array_merge(
    $auth,
    [PermissionMiddleware::class . ':' . implode(',', $permissions)]
);

$router->get('/login', AuthController::class . '@showLogin');
$router->post('/login', AuthController::class . '@login');
$router->post('/logout', AuthController::class . '@logout');

$router->get('/', DashboardController::class . '@index', $auth);
$router->get('/dashboard', DashboardController::class . '@index', $auth);
$router->get('/dashboard/stats', DashboardController::class . '@stats', $auth);

$router->get('/users', UserController::class . '@index', $withPermission('users.view', 'users.manage'));
$router->get('/users/create', UserController::class . '@create', $withPermission('users.create', 'users.manage'));
$router->post('/users', UserController::class . '@store', $withPermission('users.create', 'users.manage'));
$router->get('/users/{id}/edit', UserController::class . '@edit', $withPermission('users.view', 'users.manage'));
$router->post('/users/{id}', UserController::class . '@update', $withPermission('users.edit', 'users.manage'));
$router->post('/users/{id}/delete', UserController::class . '@delete', $withPermission('users.delete', 'users.manage'));

$router->get('/sites', SiteController::class . '@index', $withPermission('sites.view', 'sites.manage'));
$router->get('/sites/create', SiteController::class . '@create', $withPermission('sites.create', 'sites.manage'));
$router->post('/sites', SiteController::class . '@store', $withPermission('sites.create', 'sites.manage'));

$router->get('/databases', DatabaseController::class . '@index', $withPermission('databases.view', 'databases.manage'));
$router->get('/databases/create', DatabaseController::class . '@create', $withPermission('databases.create', 'databases.manage'));
$router->post('/databases', DatabaseController::class . '@store', $withPermission('databases.create', 'databases.manage'));
$router->post('/databases/{id}/delete', DatabaseController::class . '@delete', $withPermission('databases.delete', 'databases.manage'));
$router->get('/phpmyadmin/signon', DatabaseController::class . '@phpMyAdminSignon', $withPermission('databases.view', 'databases.manage'));

$router->get('/ftp', FtpController::class . '@index', $withPermission('ftp.view', 'ftp.manage'));
$router->get('/ftp/create', FtpController::class . '@create', $withPermission('ftp.create', 'ftp.manage'));
$router->post('/ftp', FtpController::class . '@store', $withPermission('ftp.create', 'ftp.manage'));
$router->post('/ftp/{id}/delete', FtpController::class . '@delete', $withPermission('ftp.delete', 'ftp.manage'));

$router->get('/cron', CronController::class . '@index', $withPermission('cron.view', 'cron.manage'));
$router->get('/cron/create', CronController::class . '@create', $withPermission('cron.create', 'cron.manage'));
$router->post('/cron', CronController::class . '@store', $withPermission('cron.create', 'cron.manage'));
$router->post('/cron/{id}/delete', CronController::class . '@delete', $withPermission('cron.delete', 'cron.manage'));

$router->get('/dns', DnsController::class . '@index', $withPermission('dns.view', 'dns.manage'));
$router->get('/dns/create', DnsController::class . '@create', $withPermission('dns.create', 'dns.manage'));
$router->post('/dns', DnsController::class . '@store', $withPermission('dns.create', 'dns.manage'));
$router->get('/dns/{id}', DnsController::class . '@show', $withPermission('dns.view', 'dns.manage'));
$router->post('/dns/{id}/records', DnsController::class . '@addRecord', $withPermission('dns.edit', 'dns.manage'));
$router->post('/dns/{domainId}/records/{recordId}/delete', DnsController::class . '@deleteRecord', $withPermission('dns.delete', 'dns.manage'));

$router->get('/terminal', TerminalController::class . '@index', $withPermission('terminal.access'));
$router->post('/terminal/start', TerminalController::class . '@start', $withPermission('terminal.access'));
$router->post('/terminal/stop', TerminalController::class . '@stop', $withPermission('terminal.access'));
$router->post('/terminal/restart', TerminalController::class . '@restart', $withPermission('terminal.access'));
$router->get('/terminal/status', TerminalController::class . '@status', $withPermission('terminal.access'));

$router->get('/auth_check', AuthController::class . '@authCheck');

$request = new Request();
$response = $router->dispatch($request);
$response->send();
