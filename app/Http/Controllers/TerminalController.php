<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Http\Session;
use App\Infrastructure\Adapters\TerminalAdapter;
use App\Infrastructure\Shell\Shell;
use App\Facades\App;
use App\Support\AuditLogger;

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
        Session::start();
        $userId   = (int) Session::get('user_id');
        $username = Session::get('username', 'unknown');

        if (!$this->terminalAdapter->isTtydInstalled()) {
            return $this->view('pages/terminal/install', [
                'title'        => 'Terminal - Installation Required',
                'instructions' => $this->terminalAdapter->getInstallationInstructions()
            ]);
        }

        if (!$this->checkTerminalAccess($userId)) {
            AuditLogger::log('terminal.access_denied', "Terminal access denied for user {$username}");
            return $this->view('pages/terminal/error', [
                'title'        => 'Terminal - Access Denied',
                'error'        => 'You do not have permission to access the terminal. Contact your administrator to request the terminal.access permission.',
                'instructions' => ''
            ]);
        }

        try {
            $role = App::roles()->getPrimaryRoleName($userId);

            $sessionInfo = $this->terminalAdapter->getSessionInfo($userId);

            if (!$sessionInfo || !$this->terminalAdapter->isSessionActive($userId)) {
                $sessionInfo = $this->terminalAdapter->startSession($userId, $role);
            } else {
                $this->terminalAdapter->updateSessionActivity($userId);
            }

            return $this->view('pages/terminal/index', [
                'title'       => 'Terminal',
                'sessionInfo' => $sessionInfo,
                'sessionTtlMinutes'  => (int) (TerminalAdapter::SESSION_TTL / 60),
                'idleTimeoutMinutes' => (int) (TerminalAdapter::IDLE_TIMEOUT / 60),
            ]);

        } catch (\Exception $e) {
            return $this->view('pages/terminal/error', [
                'title'        => 'Terminal - Error',
                'error'        => $e->getMessage(),
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
            Session::start();
            $userId = (int) Session::get('user_id');

            if (!$this->terminalAdapter->isTtydInstalled()) {
                return $this->json([
                    'error'        => 'ttyd is not installed on this system',
                    'instructions' => $this->terminalAdapter->getInstallationInstructions()
                ], 400);
            }

            if (!$this->checkTerminalAccess($userId)) {
                return $this->json(['error' => 'Access denied'], 403);
            }

            $role        = App::roles()->getPrimaryRoleName($userId);
            $sessionInfo = $this->terminalAdapter->startSession($userId, $role);

            return $this->json([
                'success' => true,
                'session' => $sessionInfo
            ]);

        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 500);
        }
    }

    /**
     * Stop the current terminal session via AJAX
     */
    public function stop(Request $request): Response
    {
        try {
            Session::start();
            $userId = (int) Session::get('user_id');

            $stopped = $this->terminalAdapter->stopSession($userId);

            return $this->json([
                'success' => true,
                'stopped' => $stopped
            ]);

        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 500);
        }
    }

    /**
     * Get current session status via AJAX and update activity timestamp
     */
    public function status(Request $request): Response
    {
        try {
            Session::start();
            $userId = (int) Session::get('user_id');

            $active      = $this->terminalAdapter->isSessionActive($userId);
            $sessionInfo = $this->terminalAdapter->getSessionInfo($userId);

            if ($active) {
                $this->terminalAdapter->updateSessionActivity($userId);
            }

            return $this->json([
                'active'         => $active,
                'session'        => $sessionInfo,
                'ttyd_installed' => $this->terminalAdapter->isTtydInstalled()
            ]);

        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 500);
        }
    }

    /**
     * Restart the terminal session via AJAX
     */
    public function restart(Request $request): Response
    {
        try {
            Session::start();
            $userId = (int) Session::get('user_id');

            if (!$this->checkTerminalAccess($userId)) {
                return $this->json(['error' => 'Access denied'], 403);
            }

            $this->terminalAdapter->stopSession($userId);

            $maxAttempts = 10;
            for ($attempt = 0; $attempt < $maxAttempts; $attempt++) {
                if (!$this->terminalAdapter->isSessionActive($userId)) {
                    break;
                }
                usleep(500000);
            }

            if ($this->terminalAdapter->isSessionActive($userId)) {
                throw new \RuntimeException('Failed to stop existing session. Please try again in a few seconds.');
            }

            $role        = App::roles()->getPrimaryRoleName($userId);
            $sessionInfo = $this->terminalAdapter->startSession($userId, $role);

            return $this->json([
                'success' => true,
                'session' => $sessionInfo
            ]);

        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 500);
        }
    }

    /**
     * Check whether the current user has the terminal.access permission.
     */
    private function checkTerminalAccess(int $userId): bool
    {
        if ($userId <= 0) {
            return false;
        }
        return App::roles()->hasPermission($userId, 'terminal.access');
    }
}

