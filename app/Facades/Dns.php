<?php

namespace App\Facades;

use App\Contracts\DnsManagerInterface;
use App\Infrastructure\Adapters\BindAdapter;
use App\Infrastructure\Adapters\TerminalAdapter;
use App\Support\Config;

class Dns
{
    private static ?DnsManagerInterface $instance = null;

    public static function getInstance(): DnsManagerInterface
    {
        if (self::$instance === null) {
            // Load BIND9 configuration from config
            Config::load('database');
            
            // Initialize BindAdapter with shell interface
            $shell = new TerminalAdapter();
            
            self::$instance = new BindAdapter(
                shell: $shell,
                zonesPath: Config::get('database.bind9.zones_path', '/etc/bind/zones'),
                namedConfPath: Config::get('database.bind9.named_conf_path', '/etc/bind/named.conf.local')
            );
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
