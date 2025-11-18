<?php

namespace App\Support;

class Config
{
    private static array $config = [];
    
    /**
     * Load a configuration file
     */
    public static function load(string $name): void
    {
        // Ensure environment is loaded before loading config files
        Env::load();
        
        $path = __DIR__ . '/../../config/' . $name . '.php';
        
        if (!file_exists($path)) {
            throw new \RuntimeException("Configuration file '{$name}' not found");
        }
        
        self::$config[$name] = require $path;
    }
    
    /**
     * Get a configuration value using dot notation
     * Example: Config::get('database.mysql.host')
     */
    public static function get(string $key, $default = null)
    {
        $parts = explode('.', $key);
        $config = self::$config;
        
        foreach ($parts as $part) {
            if (!isset($config[$part])) {
                return $default;
            }
            $config = $config[$part];
        }
        
        return $config;
    }
    
    /**
     * Check if a configuration value exists
     */
    public static function has(string $key): bool
    {
        $parts = explode('.', $key);
        $config = self::$config;
        
        foreach ($parts as $part) {
            if (!isset($config[$part])) {
                return false;
            }
            $config = $config[$part];
        }
        
        return true;
    }
    
    /**
     * Set a configuration value
     */
    public static function set(string $key, $value): void
    {
        $parts = explode('.', $key);
        $config = &self::$config;
        
        foreach ($parts as $part) {
            if (!isset($config[$part])) {
                $config[$part] = [];
            }
            $config = &$config[$part];
        }
        
        $config = $value;
    }
    
    /**
     * Get all configuration
     */
    public static function all(): array
    {
        return self::$config;
    }
}
