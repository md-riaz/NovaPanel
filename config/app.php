<?php

use App\Support\Env;

return [
    'name' => 'NovaPanel',
    'version' => '1.0.0',
    'env' => Env::get('APP_ENV', 'production'),
    'debug' => Env::get('APP_DEBUG', 'false') === 'true',
    'url' => Env::get('APP_URL', 'http://localhost:7080'),
    'timezone' => 'UTC',
    
    'session' => [
        'lifetime' => 3600, // 1 hour
        'regenerate_interval' => 300, // 5 minutes
    ],
    
    'rate_limit' => [
        'max_attempts' => 5,
        'decay_minutes' => 15,
    ],
];
