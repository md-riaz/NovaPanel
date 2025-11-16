<?php

namespace App\Facades;

use App\Contracts\DatabaseManagerInterface;
use App\Infrastructure\Adapters\MysqlDatabaseAdapter;
use App\Infrastructure\Shell\Shell;

class DatabaseManager
{
    private static ?DatabaseManagerInterface $instance = null;

    public static function getInstance(): DatabaseManagerInterface
    {
        if (self::$instance === null) {
            // TODO: Load MySQL root credentials from config
            self::$instance = new MysqlDatabaseAdapter(
                shell: new Shell(),
                host: 'localhost',
                rootUser: 'root',
                rootPassword: ''
            );
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
