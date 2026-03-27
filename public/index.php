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
$router->get('/', DashboardController::class . '@index', [AuthMiddleware::class]);
$router->get('/dashboard', DashboardController::class . '@index', [AuthMiddleware::class]);
$router->get('/dashboard/stats', DashboardController::class . '@stats', [AuthMiddleware::class]);

$router->get('/users', UserController::class . '@index', [AuthMiddleware::class]);
$router->get('/users/create', UserController::class . '@create', [AuthMiddleware::class]);
$router->post('/users', UserController::class . '@store', [AuthMiddleware::class]);
$router->get('/users/{id}/edit', UserController::class . '@edit', [AuthMiddleware::class]);
$router->post('/users/{id}', UserController::class . '@update', [AuthMiddleware::class]);
$router->post('/users/{id}/delete', UserController::class . '@delete', [AuthMiddleware::class]);

// Site routes
// Supported resource actions: index, create, store.
// Per-site show/edit/update/delete routes are intentionally not exposed because
// there are no matching controller methods or views for them yet.
$router->get('/sites', SiteController::class . '@index', [AuthMiddleware::class]);
$router->get('/sites/create', SiteController::class . '@create', [AuthMiddleware::class]);
$router->post('/sites', SiteController::class . '@store', [AuthMiddleware::class]);
$router->get('/sites/{id}', SiteController::class . '@show', [AuthMiddleware::class]);
$router->post('/sites/{id}/certificate', SiteController::class . '@requestCertificate', [AuthMiddleware::class]);
$router->post('/sites/{id}/certificate/renew', SiteController::class . '@renewCertificate', [AuthMiddleware::class]);
$router->post('/sites/{id}/certificate/reinstall', SiteController::class . '@reinstallCertificate', [AuthMiddleware::class]);
$router->post('/sites/{id}/certificate/revoke', SiteController::class . '@revokeCertificate', [AuthMiddleware::class]);
$router->post('/sites/{id}/https', SiteController::class . '@updateHttps', [AuthMiddleware::class]);

$router->get('/databases', DatabaseController::class . '@index', [AuthMiddleware::class]);
$router->get('/databases/create', DatabaseController::class . '@create', [AuthMiddleware::class]);
$router->post('/databases', DatabaseController::class . '@store', [AuthMiddleware::class]);
$router->post('/databases/{id}/delete', DatabaseController::class . '@delete', [AuthMiddleware::class]);
$router->get('/phpmyadmin/signon', DatabaseController::class . '@phpMyAdminSignon', [AuthMiddleware::class]);

// FTP routes
// Supported resource actions: index, create, store, delete.
// FTP edit/show/update routes are intentionally omitted until implemented.
$router->get('/ftp', FtpController::class . '@index', [AuthMiddleware::class]);
$router->get('/ftp/create', FtpController::class . '@create', [AuthMiddleware::class]);
$router->post('/ftp', FtpController::class . '@store', [AuthMiddleware::class]);
$router->post('/ftp/{id}/delete', FtpController::class . '@delete', [AuthMiddleware::class]);

$router->get('/cron', CronController::class . '@index', [AuthMiddleware::class]);
$router->get('/cron/create', CronController::class . '@create', [AuthMiddleware::class]);
$router->post('/cron', CronController::class . '@store', [AuthMiddleware::class]);
$router->post('/cron/{id}/delete', CronController::class . '@delete', [AuthMiddleware::class]);

$router->get('/dns', DnsController::class . '@index', [AuthMiddleware::class]);
$router->get('/dns/create', DnsController::class . '@create', [AuthMiddleware::class]);
$router->post('/dns', DnsController::class . '@store', [AuthMiddleware::class]);
$router->get('/dns/{id}', DnsController::class . '@show', [AuthMiddleware::class]);
$router->post('/dns/{id}/records', DnsController::class . '@addRecord', [AuthMiddleware::class]);
$router->post('/dns/{domainId}/records/{recordId}/delete', DnsController::class . '@deleteRecord', [AuthMiddleware::class]);

$router->get('/terminal', TerminalController::class . '@index', [AuthMiddleware::class]);
$router->post('/terminal/start', TerminalController::class . '@start', [AuthMiddleware::class]);
$router->post('/terminal/stop', TerminalController::class . '@stop', [AuthMiddleware::class]);
$router->post('/terminal/restart', TerminalController::class . '@restart', [AuthMiddleware::class]);
$router->get('/terminal/status', TerminalController::class . '@status', [AuthMiddleware::class]);

$router->get('/auth_check', AuthController::class . '@authCheck');

$request = new Request();
$response = $router->dispatch($request);
$response->send();
