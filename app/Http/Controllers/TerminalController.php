<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Infrastructure\Adapters\TerminalAdapter;
use App\Infrastructure\Shell\Shell;
use App\Support\AuditLogger;

class TerminalController extends Controller
{
    private TerminalAdapter $terminalAdapter;

    public function __construct()
    {
        $this->terminalAdapter = new TerminalAdapter(new Shell());
    }

    public function index(Request $request): Response
    {
        $userId = $this->currentUserId();
        $username = $this->currentUsername();

        if (!$this->terminalAdapter->isTtydInstalled()) {
            return $this->view('pages/terminal/install', [
                'title' => 'Terminal - Installation Required',
                'instructions' => $this->terminalAdapter->getInstallationInstructions(),
            ]);
        }

        if (!$this->checkTerminalAccess($userId)) {
            AuditLogger::log('terminal.access_denied', "Terminal access denied for user {$username}");
            return $this->view('pages/terminal/error', [
                'title' => 'Terminal - Access Denied',
                'error' => 'You do not have permission to access the terminal. Contact your administrator to request the terminal.access permission.',
                'instructions' => '',
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
                'title' => 'Terminal',
                'sessionInfo' => $sessionInfo,
                'sessionTtlMinutes' => (int) (TerminalAdapter::SESSION_TTL / 60),
                'idleTimeoutMinutes' => (int) (TerminalAdapter::IDLE_TIMEOUT / 60),
            ]);
        } catch (\Exception $e) {
            return $this->view('pages/terminal/error', [
                'title' => 'Terminal - Error',
                'error' => $e->getMessage(),
                'instructions' => $this->terminalAdapter->getInstallationInstructions(),
            ]);
        }
    }

    public function start(Request $request): Response
    {
        try {
            $userId = $this->currentUserId();

            if (!$this->terminalAdapter->isTtydInstalled()) {
                return $this->json([
                    'error' => 'ttyd is not installed on this system',
                    'instructions' => $this->terminalAdapter->getInstallationInstructions(),
                ], 400);
            }

            if (!$this->checkTerminalAccess($userId)) {
                return $this->json(['error' => 'Access denied'], 403);
            }

            return $this->json([
                'success' => true,
                'session' => $this->terminalAdapter->startSession($userId, App::roles()->getPrimaryRoleName($userId)),
            ]);
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 500);
        }
    }

    public function stop(Request $request): Response
    {
        try {
            $userId = $this->currentUserId();

            if (!$this->checkTerminalAccess($userId)) {
                return $this->json(['error' => 'Access denied'], 403);
            }

            return $this->json([
                'success' => true,
                'stopped' => $this->terminalAdapter->stopSession($userId),
            ]);
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 500);
        }
    }

    public function status(Request $request): Response
    {
        try {
            $userId = $this->currentUserId();

            if (!$this->checkTerminalAccess($userId)) {
                return $this->json(['error' => 'Access denied'], 403);
            }

            $active = $this->terminalAdapter->isSessionActive($userId);
            $sessionInfo = $this->terminalAdapter->getSessionInfo($userId);

            if ($active) {
                $this->terminalAdapter->updateSessionActivity($userId);
            }

            return $this->json([
                'active' => $active,
                'session' => $sessionInfo,
                'ttyd_installed' => $this->terminalAdapter->isTtydInstalled(),
            ]);
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 500);
        }
    }

    public function restart(Request $request): Response
    {
        try {
            $userId = $this->currentUserId();

            if (!$this->checkTerminalAccess($userId)) {
                return $this->json(['error' => 'Access denied'], 403);
            }

            $this->terminalAdapter->stopSession($userId);

            for ($attempt = 0; $attempt < 10; $attempt++) {
                if (!$this->terminalAdapter->isSessionActive($userId)) {
                    break;
                }
                usleep(500000);
            }

            if ($this->terminalAdapter->isSessionActive($userId)) {
                throw new \RuntimeException('Failed to stop existing session. Please try again in a few seconds.');
            }

            return $this->json([
                'success' => true,
                'session' => $this->terminalAdapter->startSession($userId, App::roles()->getPrimaryRoleName($userId)),
            ]);
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 500);
        }
    }

    private function checkTerminalAccess(int $userId): bool
    {
        return $userId > 0 && App::roles()->hasPermission($userId, 'terminal.access');
    }
}
