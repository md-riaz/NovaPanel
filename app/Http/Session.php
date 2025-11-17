<?php

namespace App\Http;

class Session
{
    private const SESSION_LIFETIME = 3600; // 1 hour
    private const REGENERATE_INTERVAL = 300; // 5 minutes
    
    public static function start(): void
    {
        if (session_status() === PHP_SESSION_NONE) {
            // Secure session configuration
            ini_set('session.cookie_httponly', '1');
            
            // Only enable secure cookies if using HTTPS
            // This allows the panel to work over HTTP during development/initial setup
            $isHttps = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') 
                    || (!empty($_SERVER['HTTP_X_FORWARDED_PROTO']) && $_SERVER['HTTP_X_FORWARDED_PROTO'] === 'https')
                    || (!empty($_SERVER['SERVER_PORT']) && $_SERVER['SERVER_PORT'] == 443);
            ini_set('session.cookie_secure', $isHttps ? '1' : '0');
            
            ini_set('session.cookie_samesite', 'Strict');
            ini_set('session.use_strict_mode', '1');
            ini_set('session.use_only_cookies', '1');
            ini_set('session.gc_maxlifetime', (string) self::SESSION_LIFETIME);
            
            session_name('NOVAPANEL_SESSION');
            session_start();
            
            // Regenerate session ID periodically
            self::regenerateIfNeeded();
            
            // Validate session
            self::validate();
        }
    }
    
    public static function set(string $key, $value): void
    {
        self::start();
        $_SESSION[$key] = $value;
    }
    
    public static function get(string $key, $default = null)
    {
        self::start();
        return $_SESSION[$key] ?? $default;
    }
    
    public static function has(string $key): bool
    {
        self::start();
        return isset($_SESSION[$key]);
    }
    
    public static function remove(string $key): void
    {
        self::start();
        unset($_SESSION[$key]);
    }
    
    public static function destroy(): void
    {
        // Start session without validation if not already started
        if (session_status() === PHP_SESSION_NONE) {
            session_name('NOVAPANEL_SESSION');
            @session_start();
        }
        
        $_SESSION = [];
        
        // Destroy session if it exists
        if (session_status() === PHP_SESSION_ACTIVE) {
            session_destroy();
        }
    }
    
    public static function regenerate(): void
    {
        self::start();
        session_regenerate_id(true);
        $_SESSION['_regenerated_at'] = time();
    }
    
    private static function regenerateIfNeeded(): void
    {
        $lastRegeneration = $_SESSION['_regenerated_at'] ?? 0;
        
        if (time() - $lastRegeneration > self::REGENERATE_INTERVAL) {
            session_regenerate_id(true);
            $_SESSION['_regenerated_at'] = time();
        }
    }
    
    private static function validate(): void
    {
        // Validate user agent and IP (basic fingerprinting)
        if (!self::has('_fingerprint')) {
            self::set('_fingerprint', self::generateFingerprint());
        } elseif (self::get('_fingerprint') !== self::generateFingerprint()) {
            // Session hijacking attempt detected
            self::destroy();
            throw new \RuntimeException('Session validation failed');
        }
        
        // Check session timeout
        $lastActivity = self::get('_last_activity', time());
        if (time() - $lastActivity > self::SESSION_LIFETIME) {
            self::destroy();
            throw new \RuntimeException('Session expired');
        }
        
        self::set('_last_activity', time());
    }
    
    private static function generateFingerprint(): string
    {
        return hash('sha256', 
            ($_SERVER['HTTP_USER_AGENT'] ?? '') . 
            ($_SERVER['REMOTE_ADDR'] ?? '')
        );
    }
}
