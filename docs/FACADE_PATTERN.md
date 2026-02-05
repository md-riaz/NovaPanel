# Facade Pattern Implementation Guide

## Overview

NovaPanel implements a comprehensive facade pattern to hide internal complexity and provide a clean, simple interface to the application's core functionality. This document explains the architecture and how to use the facades effectively.

## Architecture Layers

### 1. **Contracts Layer** (`app/Contracts/`)
Defines interfaces that establish contracts for infrastructure components:
- `WebServerManagerInterface` - Web server operations (Nginx)
- `DatabaseManagerInterface` - Database operations (MySQL)
- `DnsManagerInterface` - DNS operations (BIND9)
- `FtpManagerInterface` - FTP operations (Pure-FTPd)
- `PhpRuntimeManagerInterface` - PHP version management
- `CronManagerInterface` - Cron job management
- `ShellInterface` - System command execution

### 2. **Infrastructure Layer** (`app/Infrastructure/`)
Concrete implementations of contracts:
- **Adapters**: `NginxAdapter`, `MysqlDatabaseAdapter`, `BindAdapter`, `PureFtpdAdapter`, etc.
- **Shell**: Secure command execution wrapper

### 3. **Facade Layer** (`app/Facades/`)
Provides static access to complex subsystems:

#### Infrastructure Facades
- **WebServer**: Nginx configuration management
- **DatabaseManager**: MySQL database operations
- **Dns**: BIND9 DNS zone management
- **Ftp**: FTP user management
- **PhpRuntime**: PHP version management
- **Cron**: Cron job management

#### Application Facade
- **App**: Unified access to repositories and services

#### System Facades
- **Env**: Environment configuration management
- **Config**: Application configuration access

## Usage Examples

### Using Infrastructure Facades

```php
use App\Facades\WebServer;
use App\Facades\DatabaseManager;
use App\Facades\Dns;

// Create a site configuration
WebServer::createSite($site);

// Create a database
DatabaseManager::createDatabase($database);

// Setup DNS zone
Dns::createZone($domain);
```

### Using the App Facade

The `App` facade provides centralized access to repositories and services:

#### Accessing Repositories (Singleton Pattern)
```php
use App\Facades\App;

// Get all users
$users = App::users()->all();

// Find a specific site
$site = App::sites()->find($id);

// Get all databases
$databases = App::databases()->all();

// Access other repositories
$ftpUsers = App::ftpUsers()->all();
$cronJobs = App::cronJobs()->all();
$domains = App::domains()->all();
$roles = App::roles()->all();
```

#### Creating Services (Factory Pattern)
```php
use App\Facades\App;

// Create a site with all dependencies automatically injected
$service = App::createSiteService();
$site = $service->execute($userId, $domain, $phpVersion, $sslEnabled);

// Create a database
$service = App::createDatabaseService();
$database = $service->execute($userId, $dbName, $dbType, $dbUsername, $dbPassword);

// Create FTP user
$service = App::createFtpUserService();
$ftpUser = $service->execute($userId, $ftpUsername, $password, $homeDirectory);

// Add cron job
$service = App::addCronJobService();
$cronJob = $service->execute($userId, $schedule, $command, $enabled);

// Setup DNS zone
$service = App::setupDnsZoneService();
$domain = $service->execute($siteId, $domainName, $serverIp);
```

### Environment Configuration

```php
use App\Support\Env;

// Get environment values
$mysqlHost = Env::get('MYSQL_HOST', 'localhost');
$appEnv = Env::get('APP_ENV', 'production');
$appDebug = Env::get('APP_DEBUG', 'false') === 'true';

// Check if a value exists
if (Env::has('MYSQL_ROOT_PASSWORD')) {
    // Do something
}

// Get all environment values
$allEnv = Env::all();
```

### Application Configuration

```php
use App\Support\Config;

// Load a config file (auto-loads environment)
Config::load('database');
Config::load('app');

// Access configuration values with dot notation
$mysqlHost = Config::get('database.mysql.host');
$appName = Config::get('app.name');
$appUrl = Config::get('app.url', 'http://localhost:7080');
```

## Controller Best Practices

### ✅ DO: Use Facades

```php
namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Facades\App;
use App\Support\AuditLogger;

class SiteController extends Controller
{
    public function index(Request $request): Response
    {
        $sites = App::sites()->all();
        
        foreach ($sites as $site) {
            $user = App::users()->find($site->userId);
            $site->ownerUsername = $user ? $user->username : 'Unknown';
        }
        
        return $this->view('pages/sites/index', [
            'title' => 'Sites',
            'sites' => $sites
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $service = App::createSiteService();
            $site = $service->execute(
                $userId, 
                $domain, 
                $phpVersion, 
                $sslEnabled
            );
            
            AuditLogger::logCreated('site', $domain, [...]);
            return $this->redirect('/sites');
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
```

### ❌ DON'T: Instantiate Repositories/Services Directly

```php
// AVOID THIS - Violates abstraction
$userRepo = new UserRepository();
$siteRepo = new SiteRepository();
$users = $userRepo->all();

// AVOID THIS - Manual dependency construction
$service = new CreateSiteService(
    new SiteRepository(),
    new UserRepository(),
    WebServer::getInstance(),
    PhpRuntime::getInstance(),
    new Shell()
);
```

## Benefits of This Architecture

### 1. **Separation of Concerns**
- Controllers focus on HTTP request/response handling
- Business logic stays in services
- Infrastructure details hidden behind adapters
- Database access encapsulated in repositories

### 2. **Testability**
- Easy to mock facades in tests
- Contracts allow dependency injection
- Clear boundaries between layers

### 3. **Maintainability**
- Changes to infrastructure only affect adapters
- Single point of configuration for each subsystem
- Consistent API across the application

### 4. **Security**
- Shell commands centralized and validated
- Environment secrets managed securely
- No direct system calls from controllers

### 5. **Flexibility**
- Easy to swap implementations (e.g., Apache instead of Nginx)
- Support for multiple database types
- Plugin-friendly architecture

## Environment Configuration (.env.php)

The `.env.php` file returns an array of configuration values:

```php
<?php

return [
    // MySQL Credentials
    'MYSQL_HOST' => 'localhost',
    'MYSQL_ROOT_USER' => 'novapanel_db',
    'MYSQL_ROOT_PASSWORD' => 'your_secure_password',
    
    // Application Settings
    'APP_ENV' => 'production',
    'APP_DEBUG' => 'false',
    'APP_URL' => 'http://your-server-ip:7080',
    
    // Add more configuration as needed
];
```

**No getenv() or putenv() needed!** The Env class manages everything internally.

## Bootstrap Process

The application bootstrap (`bootstrap/app.php`) handles initialization:

1. Loads Composer autoloader
2. Loads environment configuration via `Env::load()`
3. Sets error reporting based on environment
4. Returns environment info for use in the application

Entry point (`public/index.php`) simply requires the bootstrap:

```php
<?php

// Bootstrap the application
require_once __DIR__ . '/../bootstrap/app.php';

// Rest of application routing...
```

## Adding New Facades

### For Infrastructure Components

1. **Create Interface** in `app/Contracts/`:
```php
<?php
namespace App\Contracts;

interface NewServiceInterface {
    public function doSomething(): bool;
}
```

2. **Create Adapter** in `app/Infrastructure/Adapters/`:
```php
<?php
namespace App\Infrastructure\Adapters;

use App\Contracts\NewServiceInterface;

class NewServiceAdapter implements NewServiceInterface {
    public function doSomething(): bool {
        // Implementation
    }
}
```

3. **Create Facade** in `app/Facades/`:
```php
<?php
namespace App\Facades;

use App\Contracts\NewServiceInterface;
use App\Infrastructure\Adapters\NewServiceAdapter;

class NewService {
    private static ?NewServiceInterface $instance = null;
    
    public static function getInstance(): NewServiceInterface {
        if (self::$instance === null) {
            self::$instance = new NewServiceAdapter();
        }
        return self::$instance;
    }
    
    public static function __callStatic(string $method, array $args) {
        return self::getInstance()->$method(...$args);
    }
}
```

4. **Use in Code**:
```php
use App\Facades\NewService;

NewService::doSomething();
```

### For Repositories

Add to `App` facade in `app/Facades/App.php`:

```php
private static ?NewRepository $newRepository = null;

public static function newThings(): NewRepository {
    if (self::$newRepository === null) {
        self::$newRepository = new NewRepository();
    }
    return self::$newRepository;
}
```

## Summary

The facade pattern in NovaPanel provides:
- ✅ Clean, simple API for complex operations
- ✅ Centralized configuration management
- ✅ Strong separation of concerns
- ✅ Easy testing and maintenance
- ✅ Secure, consistent access to system resources
- ✅ No manual dependency construction needed

By using facades consistently throughout the codebase, we maintain a clean architecture that's easy to understand, test, and extend.
