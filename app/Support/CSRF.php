<?php

namespace App\Support;

use App\Http\Session;

class CSRF
{
    private const TOKEN_NAME = '_csrf_token';
    
    /**
     * Generate a new CSRF token
     */
    public static function generateToken(): string
    {
        $token = bin2hex(random_bytes(32));
        Session::set(self::TOKEN_NAME, $token);
        return $token;
    }
    
    /**
     * Get the current CSRF token
     */
    public static function getToken(): string
    {
        if (!Session::has(self::TOKEN_NAME)) {
            return self::generateToken();
        }
        
        return Session::get(self::TOKEN_NAME);
    }
    
    /**
     * Verify the CSRF token
     */
    public static function verify(string $token): bool
    {
        $sessionToken = Session::get(self::TOKEN_NAME);
        
        if (!$sessionToken) {
            return false;
        }
        
        return hash_equals($sessionToken, $token);
    }
    
    /**
     * Get CSRF token as hidden input field
     */
    public static function field(): string
    {
        $token = self::getToken();
        return sprintf(
            '<input type="hidden" name="%s" value="%s">',
            self::TOKEN_NAME,
            htmlspecialchars($token, ENT_QUOTES, 'UTF-8')
        );
    }
    
    /**
     * Get CSRF token as meta tag
     */
    public static function meta(): string
    {
        $token = self::getToken();
        return sprintf(
            '<meta name="csrf-token" content="%s">',
            htmlspecialchars($token, ENT_QUOTES, 'UTF-8')
        );
    }
}
