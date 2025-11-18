<?php

namespace App\Support;

/**
 * Environment Configuration Manager
 * 
 * Centralized class for loading and accessing environment variables.
 * Ensures .env.php is loaded only once and provides clean access to env values.
 */
class Env
{
    private static bool $loaded = false;
    
    /**
     * Load environment configuration from .env.php
     * This should be called once at application bootstrap
     */
    public static function load(): void
    {
        if (self::$loaded) {
            return; // Already loaded, prevent duplicate loading
        }
        
        $envFile = self::getEnvFilePath();
        
        if (file_exists($envFile)) {
            require_once $envFile;
            self::$loaded = true;
        }
    }
    
    /**
     * Get an environment variable value
     * 
     * @param string $key The environment variable name
     * @param mixed $default Default value if not found
     * @return mixed
     */
    public static function get(string $key, $default = null)
    {
        // Ensure env is loaded
        self::load();
        
        $value = getenv($key);
        
        // getenv returns false if variable doesn't exist
        if ($value === false) {
            return $default;
        }
        
        return $value;
    }
    
    /**
     * Check if an environment variable exists
     */
    public static function has(string $key): bool
    {
        self::load();
        return getenv($key) !== false;
    }
    
    /**
     * Set an environment variable (mainly for testing)
     */
    public static function set(string $key, string $value): void
    {
        putenv("$key=$value");
    }
    
    /**
     * Get the path to the .env.php file
     */
    private static function getEnvFilePath(): string
    {
        return __DIR__ . '/../../.env.php';
    }
    
    /**
     * Check if environment configuration is loaded
     */
    public static function isLoaded(): bool
    {
        return self::$loaded;
    }
}
