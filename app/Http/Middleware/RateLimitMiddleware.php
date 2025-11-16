<?php

namespace App\Http\Middleware;

use App\Http\Request;
use App\Http\Response;
use App\Support\RateLimiter;

class RateLimitMiddleware
{
    public function handle(Request $request, callable $next): Response
    {
        $key = $this->getKey($request);
        
        if (RateLimiter::tooManyAttempts($key)) {
            $seconds = RateLimiter::availableIn($key);
            
            return (new Response())->json([
                'error' => 'Too many attempts. Please try again later.',
                'retry_after' => $seconds
            ], 429);
        }
        
        // Increment attempt counter for certain routes
        if ($this->shouldRateLimit($request)) {
            RateLimiter::hit($key);
        }
        
        return $next($request);
    }
    
    private function getKey(Request $request): string
    {
        return sprintf(
            'route:%s:ip:%s',
            $request->path(),
            $request->server('REMOTE_ADDR', 'unknown')
        );
    }
    
    private function shouldRateLimit(Request $request): bool
    {
        // Apply rate limiting to sensitive routes
        $sensitiveRoutes = ['/login', '/register', '/password-reset'];
        
        foreach ($sensitiveRoutes as $route) {
            if (str_starts_with($request->path(), $route)) {
                return true;
            }
        }
        
        return false;
    }
}
