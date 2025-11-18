<?php

namespace App\Support;

/**
 * Environment Configuration Manager
 * 
 * Centralized class for loading and accessing environment variables.
 * Ensures .env.php is loaded only once and provides clean access to env values.
 * No need for getenv()/putenv() - directly manages configuration array.
 */
class Env
{
    private static bool $loaded = false;
    private static array $values = [];
    
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
            $envConfig = require $envFile;
            
            // Support both array return and putenv() style for backward compatibility
            if (is_array($envConfig)) {
                self::$values = $envConfig;
            }
            
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
        
        return self::$values[$key] ?? $default;
    }
    
    /**
     * Check if an environment variable exists
     */
    public static function has(string $key): bool
    {
        self::load();
        return isset(self::$values[$key]);
    }
    
    /**
     * Set an environment variable (mainly for testing)
     */
    public static function set(string $key, $value): void
    {
        self::$values[$key] = $value;
    }
    
    /**
     * Get all environment values
     */
    public static function all(): array
    {
        self::load();
        return self::$values;
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
