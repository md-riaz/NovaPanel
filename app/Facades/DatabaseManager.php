<?php

namespace App\Facades;

use App\Contracts\DatabaseManagerInterface;
use App\Infrastructure\Adapters\MysqlDatabaseAdapter;
use App\Infrastructure\Shell\Shell;
use App\Support\Config;

class DatabaseManager
{
    private static ?DatabaseManagerInterface $instance = null;

    public static function getInstance(): DatabaseManagerInterface
    {
        if (self::$instance === null) {
            // Load MySQL root credentials from config
            Config::load('database');
            
            self::$instance = new MysqlDatabaseAdapter(
                shell: new Shell(),
                host: Config::get('database.mysql.host', 'localhost'),
                rootUser: Config::get('database.mysql.root_user', 'root'),
                rootPassword: Config::get('database.mysql.root_password', '')
            );
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
