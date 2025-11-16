<?php

require_once __DIR__ . '/../vendor/autoload.php';

use App\Http\Router;
use App\Http\Request;
use App\Http\Controllers\DashboardController;
use App\Http\Controllers\SiteController;
use App\Http\Controllers\UserController;
use App\Http\Controllers\TerminalController;

$router = new Router();

// Dashboard routes
$router->get('/', DashboardController::class . '@index');
$router->get('/dashboard', DashboardController::class . '@index');

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
