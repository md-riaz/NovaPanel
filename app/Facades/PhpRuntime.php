<?php

namespace App\Facades;

use App\Contracts\PhpRuntimeManagerInterface;
use App\Infrastructure\Adapters\PhpFpmAdapter;
use App\Infrastructure\Shell\Shell;

class PhpRuntime
{
    private static ?PhpRuntimeManagerInterface $instance = null;

    public static function getInstance(): PhpRuntimeManagerInterface
    {
        if (self::$instance === null) {
            self::$instance = new PhpFpmAdapter(new Shell());
        }
        
        return self::$instance;
    }

    public static function __callStatic(string $method, array $args)
    {
        return self::getInstance()->$method(...$args);
    }
}
