<?php

namespace App\Facades;

use App\Contracts\DnsManagerInterface;
use App\Infrastructure\Adapters\PowerDnsAdapter;
use App\Support\Config;

class Dns
{
    private static ?DnsManagerInterface $instance = null;

    public static function getInstance(): DnsManagerInterface
    {
        if (self::$instance === null) {
            // Load PowerDNS credentials from config
            Config::load('database');
            
            self::$instance = new PowerDnsAdapter(
                host: Config::get('database.powerdns.host', 'localhost'),
                database: Config::get('database.powerdns.database', 'powerdns'),
                username: Config::get('database.powerdns.username', 'powerdns'),
                password: Config::get('database.powerdns.password', '')
            );
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
