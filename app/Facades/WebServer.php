<?php

namespace App\Facades;

use App\Contracts\WebServerManagerInterface;
use App\Infrastructure\Adapters\NginxAdapter;
use App\Infrastructure\Shell\Shell;

class WebServer
{
    private static ?WebServerManagerInterface $instance = null;

    public static function getInstance(): WebServerManagerInterface
    {
        if (self::$instance === null) {
            self::$instance = new NginxAdapter(new Shell());
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
