<?php

require_once __DIR__ . '/../vendor/autoload.php';

use App\Http\Router;
use App\Http\Request;
use App\Http\Controllers\DashboardController;
use App\Http\Controllers\SiteController;
use App\Http\Controllers\AccountController;
use App\Http\Controllers\UserController;

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

// Account routes
$router->get('/accounts', AccountController::class . '@index');
$router->get('/accounts/create', AccountController::class . '@create');
$router->post('/accounts', AccountController::class . '@store');

// Site routes
$router->get('/sites', SiteController::class . '@index');
$router->get('/sites/create', SiteController::class . '@create');
$router->post('/sites', SiteController::class . '@store');

// Dispatch request
$request = new Request();
$response = $router->dispatch($request);
$response->send();
