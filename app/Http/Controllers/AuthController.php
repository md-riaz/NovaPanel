<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Http\Session;
use App\Facades\App;
use App\Support\RateLimiter;
use App\Support\CSRF;

class AuthController extends Controller
{
    /**
     * Nginx auth_request endpoint: returns 200 if user is logged in, 401 otherwise
     */
    public function authCheck(Request $request): Response
    {
        Session::start();
        if (!Session::has('user_id')) {
            return new Response('', 401);
        }
        $userId = Session::get('user_id');
        $roleRepo = \App::roles();
        $roles = $roleRepo->getUserRoles($userId);
        $hasAdmin = false;
        foreach ($roles as $role) {
            if (strtolower($role->name) === 'admin') {
                $hasAdmin = true;
                break;
            }
        }
        if (!$hasAdmin) {
            return new Response('', 403);
        }
        return new Response('', 200);
    }
     * Show login form
     */
    public function showLogin(Request $request): Response
    {
        // If already logged in, redirect to dashboard
        if (Session::has('user_id')) {
            return $this->redirect('/dashboard');
        }
        
        return $this->view('pages/auth/login', [
            'title' => 'Login'
        ]);
    }
    
    /**
     * Handle login request
     */
    public function login(Request $request): Response
    {
        try {
            $username = $request->post('username');
            $password = $request->post('password');
            $csrfToken = $request->post('_csrf_token');
            
            // Verify CSRF token
            if (!CSRF::verify($csrfToken)) {
                throw new \Exception('Invalid CSRF token. Please refresh the page and try again.');
            }
            
            // Rate limiting
            $key = 'login:' . $request->server('REMOTE_ADDR', 'unknown');
            
            if (RateLimiter::tooManyAttempts($key)) {
                $seconds = RateLimiter::availableIn($key);
                $minutes = ceil($seconds / 60);
                throw new \Exception("Too many login attempts. Please try again in {$minutes} minute(s).");
            }
            
            // Validate input
            if (empty($username) || empty($password)) {
                throw new \Exception('Username and password are required');
            }
            
            // Find user
            $user = App::users()->findByUsername($username);
            
            if (!$user) {
                // Increment rate limit counter on failed attempt
                RateLimiter::hit($key);
                throw new \Exception('Invalid username or password');
            }
            
            // Verify password
            if (!password_verify($password, $user->password)) {
                // Increment rate limit counter on failed attempt
                RateLimiter::hit($key);
                throw new \Exception('Invalid username or password');
            }
            
            // Clear rate limiter on successful login
            RateLimiter::clear($key);
            
            // Start session and store user data
            Session::start();
            Session::set('user_id', $user->id);
            Session::set('username', $user->username);
            Session::set('email', $user->email);
            Session::regenerate();
            
            // Log the login event
            $this->logAudit('user.login', "User '{$user->username}' logged in", [
                'user_id' => $user->id,
                'ip' => $request->server('REMOTE_ADDR', 'unknown')
            ]);
            
            return $this->redirect('/dashboard');
            
        } catch (\Exception $e) {
            // Return to login page with error
            return $this->view('pages/auth/login', [
                'title' => 'Login',
                'error' => $e->getMessage()
            ]);
        }
    }
    
    /**
     * Handle logout request
     */
    public function logout(Request $request): Response
    {
        Session::start();
        
        // Verify CSRF token
        $csrfToken = $request->post('_csrf_token');
        if (!CSRF::verify($csrfToken)) {
            // Invalid CSRF token - still logout but log the attempt
            $this->logAudit('security.csrf_failed', "CSRF token verification failed on logout", [
                'ip' => $request->server('REMOTE_ADDR', 'unknown')
            ]);
        }
        
        $username = Session::get('username', 'unknown');
        
        // Log the logout event
        $this->logAudit('user.logout', "User '{$username}' logged out", [
            'user_id' => Session::get('user_id'),
            'ip' => $request->server('REMOTE_ADDR', 'unknown')
        ]);
        
        // Destroy session
        Session::destroy();
        
        return $this->redirect('/login');
    }
    
    /**
     * Log audit event
     */
    private function logAudit(string $action, string $message, array $context = []): void
    {
        $logFile = __DIR__ . '/../../../storage/logs/audit.log';
        $timestamp = date('Y-m-d H:i:s');
        $contextJson = json_encode($context);
        
        $logMessage = sprintf(
            "[%s] ACTION=%s MESSAGE=%s CONTEXT=%s\n",
            $timestamp,
            $action,
            $message,
            $contextJson
        );
        
        @file_put_contents($logFile, $logMessage, FILE_APPEND);
    }
}
