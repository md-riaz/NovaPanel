<?php

namespace App\Http\Middleware;

use App\Http\Request;
use App\Http\Response;
use App\Http\Session;

class AuthMiddleware
{
    /**
     * Handle an incoming request
     */
    public function handle(Request $request, callable $next): Response
    {
        Session::start();
        
        // Check if user is authenticated
        if (!$this->isAuthenticated()) {
            // Redirect to login page
            return $this->redirectToLogin();
        }
        
        return $next($request);
    }
    
    /**
     * Check if user is authenticated
     */
    private function isAuthenticated(): bool
    {
        return Session::has('user_id') && Session::get('user_id') !== null;
    }
    
    /**
     * Redirect to login page
     */
    private function redirectToLogin(): Response
    {
        // Return a redirect response
        header('Location: /login');
        exit;
    }
}
