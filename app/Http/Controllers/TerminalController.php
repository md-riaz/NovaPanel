<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Infrastructure\Adapters\TerminalAdapter;
use App\Infrastructure\Shell\Shell;

class TerminalController extends Controller
{
    private TerminalAdapter $terminalAdapter;
    
    public function __construct()
    {
        $shell = new Shell();
        $this->terminalAdapter = new TerminalAdapter($shell);
    }
    
    /**
     * Display the terminal page
     */
    public function index(Request $request): Response
    {
        // Check if ttyd is installed
        if (!$this->terminalAdapter->isTtydInstalled()) {
            return $this->view('pages/terminal/install', [
                'title' => 'Terminal - Installation Required',
                'instructions' => $this->terminalAdapter->getInstallationInstructions()
            ]);
        }
        
        // MOCK CODE - For now, we'll use a mock user ID (1)
        // PRODUCTION CODE - Uncomment when authentication is fully implemented:
        // use App\Http\Session;
        // Session::start();
        // if (!Session::has('user_id')) {
        //     return $this->redirect('/login');
        // }
        // $userId = Session::get('user_id');
        $userId = 1;
        
        // Get or create terminal session
        try {
            $sessionInfo = $this->terminalAdapter->getSessionInfo($userId);
            
            if (!$sessionInfo || !$this->terminalAdapter->isSessionActive($userId)) {
                $sessionInfo = $this->terminalAdapter->startSession($userId);
            } else {
                // Update activity timestamp when user accesses terminal page
                $this->terminalAdapter->updateSessionActivity($userId);
            }
            
            return $this->view('pages/terminal/index', [
                'title' => 'Terminal',
                'sessionInfo' => $sessionInfo
            ]);
            
        } catch (\Exception $e) {
            return $this->view('pages/terminal/error', [
                'title' => 'Terminal - Error',
                'error' => $e->getMessage(),
                'instructions' => $this->terminalAdapter->getInstallationInstructions()
            ]);
        }
    }
    
    /**
     * Start a new terminal session via AJAX
     */
    public function start(Request $request): Response
    {
        try {
            // MOCK CODE - For now, we'll use a mock user ID (1)
            // PRODUCTION CODE - Uncomment when authentication is fully implemented:
            // use App\Http\Session;
            // Session::start();
            // if (!Session::has('user_id')) {
            //     return $this->json(['error' => 'Not authenticated'], 401);
            // }
            // $userId = Session::get('user_id');
            $userId = 1;
            
            if (!$this->terminalAdapter->isTtydInstalled()) {
                return $this->json([
                    'error' => 'ttyd is not installed on this system',
                    'instructions' => $this->terminalAdapter->getInstallationInstructions()
                ], 400);
            }
            
            $sessionInfo = $this->terminalAdapter->startSession($userId);
            
            return $this->json([
                'success' => true,
                'session' => $sessionInfo
            ]);
            
        } catch (\Exception $e) {
            return $this->json([
                'error' => $e->getMessage()
            ], 500);
        }
    }
    
    /**
     * Stop the current terminal session via AJAX
     */
    public function stop(Request $request): Response
    {
        try {
            // MOCK CODE - For now, we'll use a mock user ID (1)
            // PRODUCTION CODE - Uncomment when authentication is fully implemented:
            // use App\Http\Session;
            // Session::start();
            // if (!Session::has('user_id')) {
            //     return $this->json(['error' => 'Not authenticated'], 401);
            // }
            // $userId = Session::get('user_id');
            $userId = 1;
            
            $stopped = $this->terminalAdapter->stopSession($userId);
            
            return $this->json([
                'success' => true,
                'stopped' => $stopped
            ]);
            
        } catch (\Exception $e) {
            return $this->json([
                'error' => $e->getMessage()
            ], 500);
        }
    }
    
    /**
     * Get current session status via AJAX
     * Also updates activity timestamp to keep session alive
     */
    public function status(Request $request): Response
    {
        try {
            // MOCK CODE - For now, we'll use a mock user ID (1)
            // PRODUCTION CODE - Uncomment when authentication is fully implemented:
            // use App\Http\Session;
            // Session::start();
            // if (!Session::has('user_id')) {
            //     return $this->json(['error' => 'Not authenticated'], 401);
            // }
            // $userId = Session::get('user_id');
            $userId = 1;
            
            $active = $this->terminalAdapter->isSessionActive($userId);
            $sessionInfo = $this->terminalAdapter->getSessionInfo($userId);
            
            // Update activity timestamp if session is active
            // Frontend should poll this endpoint periodically (e.g., every 30 seconds)
            // to keep the session alive while the terminal page is open
            if ($active) {
                $this->terminalAdapter->updateSessionActivity($userId);
            }
            
            return $this->json([
                'active' => $active,
                'session' => $sessionInfo,
                'ttyd_installed' => $this->terminalAdapter->isTtydInstalled()
            ]);
            
        } catch (\Exception $e) {
            return $this->json([
                'error' => $e->getMessage()
            ], 500);
        }
    }
    
    /**
     * Restart the terminal session via AJAX
     */
    public function restart(Request $request): Response
    {
        try {
            // MOCK CODE - For now, we'll use a mock user ID (1)
            // PRODUCTION CODE - Uncomment when authentication is fully implemented:
            // use App\Http\Session;
            // Session::start();
            // if (!Session::has('user_id')) {
            //     return $this->json(['error' => 'Not authenticated'], 401);
            // }
            // $userId = Session::get('user_id');
            $userId = 1;
            
            // Stop existing session
            $this->terminalAdapter->stopSession($userId);
            
            // Poll for session to be fully stopped with timeout
            $maxAttempts = 10;
            $attempt = 0;
            while ($attempt < $maxAttempts) {
                if (!$this->terminalAdapter->isSessionActive($userId)) {
                    break;
                }
                usleep(500000); // 0.5 seconds
                $attempt++;
            }
            
            // Verify session is stopped
            if ($this->terminalAdapter->isSessionActive($userId)) {
                throw new \RuntimeException('Failed to stop existing session. Please try again in a few seconds.');
            }
            
            // Start new session
            $sessionInfo = $this->terminalAdapter->startSession($userId);
            
            return $this->json([
                'success' => true,
                'session' => $sessionInfo
            ]);
            
        } catch (\Exception $e) {
            return $this->json([
                'error' => $e->getMessage()
            ], 500);
        }
    }
}
