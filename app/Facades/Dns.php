<?php

namespace App\Facades;

use App\Contracts\DnsManagerInterface;
use App\Infrastructure\Adapters\PowerDnsAdapter;

class Dns
{
    private static ?DnsManagerInterface $instance = null;

    public static function getInstance(): DnsManagerInterface
    {
        if (self::$instance === null) {
            // TODO: Load PowerDNS credentials from config
            self::$instance = new PowerDnsAdapter(
                host: 'localhost',
                database: 'powerdns',
                username: 'powerdns',
                password: ''
            );
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
