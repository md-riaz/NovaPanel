<?php

require_once __DIR__ . '/../vendor/autoload.php';

use App\Http\Router;
use App\Http\Request;
use App\Http\Controllers\DashboardController;
use App\Http\Controllers\SiteController;
use App\Http\Controllers\AccountController;

$router = new Router();

// Dashboard routes
$router->get('/', DashboardController::class . '@index');
$router->get('/dashboard', DashboardController::class . '@index');

// Site routes
$router->get('/sites', SiteController::class . '@index');
$router->get('/sites/create', SiteController::class . '@create');
$router->post('/sites', SiteController::class . '@store');

// Account routes
$router->get('/accounts', AccountController::class . '@index');
$router->get('/accounts/create', AccountController::class . '@create');
$router->post('/accounts', AccountController::class . '@store');

// Dispatch request
$request = new Request();
$response = $router->dispatch($request);
$response->send();
