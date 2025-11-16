<?php

namespace App\Support;

class RateLimiter
{
    private const CACHE_DIR = __DIR__ . '/../../storage/cache';
    private const MAX_ATTEMPTS = 5;
    private const DECAY_MINUTES = 15;
    
    /**
     * Check if the given key has exceeded the rate limit
     */
    public static function tooManyAttempts(string $key): bool
    {
        return self::attempts($key) >= self::MAX_ATTEMPTS;
    }
    
    /**
     * Increment the counter for a given key
     */
    public static function hit(string $key, int $decayMinutes = self::DECAY_MINUTES): int
    {
        $cacheKey = self::getCacheKey($key);
        $data = self::getCache($cacheKey);
        
        $attempts = ($data['attempts'] ?? 0) + 1;
        $expiresAt = $data['expires_at'] ?? time() + ($decayMinutes * 60);
        
        self::setCache($cacheKey, [
            'attempts' => $attempts,
            'expires_at' => $expiresAt
        ]);
        
        return $attempts;
    }
    
    /**
     * Get the number of attempts for the given key
     */
    public static function attempts(string $key): int
    {
        $cacheKey = self::getCacheKey($key);
        $data = self::getCache($cacheKey);
        
        if (!$data || (isset($data['expires_at']) && $data['expires_at'] < time())) {
            return 0;
        }
        
        return $data['attempts'] ?? 0;
    }
    
    /**
     * Get the number of seconds until the rate limit is reset
     */
    public static function availableIn(string $key): int
    {
        $cacheKey = self::getCacheKey($key);
        $data = self::getCache($cacheKey);
        
        if (!$data || !isset($data['expires_at'])) {
            return 0;
        }
        
        return max(0, $data['expires_at'] - time());
    }
    
    /**
     * Clear the rate limiter for the given key
     */
    public static function clear(string $key): void
    {
        $cacheKey = self::getCacheKey($key);
        $cacheFile = self::CACHE_DIR . '/' . $cacheKey;
        
        if (file_exists($cacheFile)) {
            @unlink($cacheFile);
        }
    }
    
    private static function getCacheKey(string $key): string
    {
        return 'rate_limit_' . hash('sha256', $key);
    }
    
    private static function getCache(string $key): ?array
    {
        $cacheFile = self::CACHE_DIR . '/' . $key;
        
        if (!file_exists($cacheFile)) {
            return null;
        }
        
        $content = @file_get_contents($cacheFile);
        if ($content === false) {
            return null;
        }
        
        return json_decode($content, true);
    }
    
    private static function setCache(string $key, array $data): void
    {
        if (!is_dir(self::CACHE_DIR)) {
            @mkdir(self::CACHE_DIR, 0750, true);
        }
        
        $cacheFile = self::CACHE_DIR . '/' . $key;
        @file_put_contents($cacheFile, json_encode($data));
    }
}
