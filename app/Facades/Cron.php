<?php

namespace App\Facades;

use App\Contracts\CronManagerInterface;
use App\Infrastructure\Adapters\CronAdapter;
use App\Infrastructure\Shell\Shell;

class Cron
{
    private static ?CronManagerInterface $instance = null;

    public static function getInstance(): CronManagerInterface
    {
        if (self::$instance === null) {
            self::$instance = new CronAdapter(new Shell());
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
