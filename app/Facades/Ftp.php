<?php

namespace App\Facades;

use App\Contracts\FtpManagerInterface;
use App\Infrastructure\Adapters\PureFtpdAdapter;
use App\Infrastructure\Shell\Shell;

class Ftp
{
    private static ?FtpManagerInterface $instance = null;

    public static function getInstance(): FtpManagerInterface
    {
        if (self::$instance === null) {
            self::$instance = new PureFtpdAdapter(new Shell());
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
