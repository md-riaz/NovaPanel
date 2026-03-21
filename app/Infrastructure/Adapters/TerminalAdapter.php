<?php

namespace App\Infrastructure\Adapters;

use App\Infrastructure\Shell\Shell;
use App\Repositories\TerminalSessionRepository;
use App\Domain\Entities\TerminalSession;
use App\Support\AuditLogger;

/**
 * Manages ttyd (web terminal) processes for NovaPanel users.
 *
 * Architecture:
 *   - One ttyd process per session (never shared between users)
 *   - Sessions tracked in the panel SQLite database with UUID identifiers
 *   - Each session has a TTL (max lifetime) and an idle timeout
 *   - ttyd is launched via a wrapper script that sanitizes the environment
 *     and enforces role-based shell behaviour
 *   - All sessions are proxied through the web server; ttyd ports are never
 *     exposed externally
 */
class TerminalAdapter
{
    /** Maximum session lifetime in seconds (15 minutes) */
    public const SESSION_TTL = 900;

    /** Idle timeout in seconds (5 minutes) */
    public const IDLE_TIMEOUT = 300;

    /** Base port for terminal sessions */
    private const BASE_PORT = 7100;

    /** Number of ports to scan for an available one */
    private const PORT_RANGE = 100;

    /** Path to the wrapper shell script */
    private const WRAPPER_SCRIPT = '/opt/novapanel/bin/terminal-wrapper.sh';

    private Shell $shell;
    private TerminalSessionRepository $sessionRepo;
    private string $logDir;

    public function __construct(Shell $shell, ?TerminalSessionRepository $sessionRepo = null)
    {
        $this->shell = $shell;
        $this->sessionRepo = $sessionRepo ?? new TerminalSessionRepository();

        $this->logDir = defined('NOVAPANEL_TEST_MODE')
            ? sys_get_temp_dir() . '/novapanel_terminal_logs'
            : '/opt/novapanel/storage/terminal/logs';

        if (!is_dir($this->logDir)) {
            @mkdir($this->logDir, 0750, true);
        }
    }

    // -------------------------------------------------------------------------
    // Public API
    // -------------------------------------------------------------------------

    /**
     * Start a new terminal session for the given user / role.
     *
     * If the user already has an active, non-expired, non-idle session the
     * existing session is returned unchanged.
     *
     * @param int    $userId Panel user ID
     * @param string $role   Primary role name (Admin|AccountOwner|Developer|ReadOnly)
     * @return array Session info including session_id, port and proxy URL
     * @throws \RuntimeException
     */
    public function startSession(int $userId, string $role = 'ReadOnly'): array
    {
        // Reuse an existing valid session when available
        $existing = $this->sessionRepo->findActiveByUserId($userId);
        if ($existing && $this->isSessionValid($existing)) {
            $this->sessionRepo->updateLastSeen($existing->id);
            return $this->buildSessionInfo($existing);
        }

        // Terminate any stale session record for this user before starting a new one
        if ($existing) {
            $this->terminateSession($existing);
        }

        if (!$this->isTtydInstalled()) {
            throw new \RuntimeException('ttyd is not installed. Please install ttyd first.');
        }

        $port      = $this->findAvailablePort();
        $sessionId = $this->generateUuid();
        $now       = time();

        $session           = new TerminalSession();
        $session->id       = $sessionId;
        $session->userId   = $userId;
        $session->role     = $role;
        $session->status   = 'pending';
        $session->expiresAt  = date('Y-m-d H:i:s', $now + self::SESSION_TTL);
        $session->lastSeenAt = date('Y-m-d H:i:s', $now);

        $this->sessionRepo->create($session);

        // Launch ttyd with the wrapper script
        $pid = $this->launchTtyd($port, $sessionId, $role, $userId);

        $session->ttydPort  = $port;
        $session->processId = $pid;
        $session->status    = 'active';
        $this->sessionRepo->update($session);

        AuditLogger::log('terminal.started', "Terminal session started for user {$userId}", [
            'session_id' => $sessionId,
            'role'       => $role,
            'port'       => $port,
        ]);

        return $this->buildSessionInfo($session);
    }

    /**
     * Stop and record the end of a terminal session for a user.
     *
     * @param int $userId Panel user ID
     * @return bool True if a session was found and stopped
     */
    public function stopSession(int $userId): bool
    {
        $session = $this->sessionRepo->findActiveByUserId($userId);
        if (!$session) {
            return false;
        }

        $duration = $session->createdAt
            ? (time() - strtotime($session->createdAt))
            : 0;

        $this->terminateSession($session);

        AuditLogger::log('terminal.ended', "Terminal session ended for user {$userId}", [
            'session_id' => $session->id,
            'duration'   => $duration,
        ]);

        return true;
    }

    /**
     * Check whether the user currently has a live terminal session.
     */
    public function isSessionActive(int $userId): bool
    {
        $session = $this->sessionRepo->findActiveByUserId($userId);
        if (!$session) {
            return false;
        }
        return $this->isSessionValid($session);
    }

    /**
     * Get session information for a user (returns null when no active session).
     */
    public function getSessionInfo(int $userId): ?array
    {
        $session = $this->sessionRepo->findActiveByUserId($userId);
        if (!$session || !$this->isSessionValid($session)) {
            return null;
        }
        return $this->buildSessionInfo($session);
    }

    /**
     * Update the last-seen timestamp, keeping the session alive.
     * Should be called on every /terminal/status poll.
     */
    public function updateSessionActivity(int $userId): void
    {
        $session = $this->sessionRepo->findActiveByUserId($userId);
        if ($session) {
            $this->sessionRepo->updateLastSeen($session->id);
        }
    }

    /**
     * Stop all active terminal sessions (e.g. during maintenance).
     *
     * @return int Number of sessions stopped
     */
    public function stopAllSessions(): int
    {
        $count    = 0;
        $sessions = $this->sessionRepo->findAllActive();
        foreach ($sessions as $session) {
            $this->terminateSession($session);
            $count++;
        }
        return $count;
    }

    /**
     * Get all active session user IDs.
     *
     * @return int[]
     */
    public function getActiveSessions(): array
    {
        return array_map(
            fn(TerminalSession $s) => $s->userId,
            $this->sessionRepo->findAllActive()
        );
    }

    /**
     * Clean up sessions that have expired or been idle too long.
     *
     * @param int $maxIdleSeconds Idle threshold (defaults to IDLE_TIMEOUT)
     * @return int Number of sessions cleaned up
     */
    public function cleanupStaleSessions(int $maxIdleSeconds = self::IDLE_TIMEOUT): int
    {
        $count = 0;

        // Expired sessions
        foreach ($this->sessionRepo->findExpired() as $session) {
            $this->terminateSession($session);
            $count++;
        }

        // Idle sessions
        foreach ($this->sessionRepo->findIdleSince($maxIdleSeconds) as $session) {
            // Skip sessions already caught by expiry check
            if ($session->status !== 'active') {
                continue;
            }
            $this->terminateSession($session);
            $count++;
        }

        // Purge old ended records
        $this->sessionRepo->deleteEnded();

        return $count;
    }

    /**
     * Check whether ttyd is installed on the system.
     */
    public function isTtydInstalled(): bool
    {
        $result = shell_exec('which ttyd 2>/dev/null');
        return !empty(trim($result ?? ''));
    }

    /**
     * Return human-readable ttyd installation instructions.
     */
    public function getInstallationInstructions(): string
    {
        return <<<'TEXT'
To install ttyd on Ubuntu/Debian:

Option 1 - From package (Ubuntu 20.04+):
  sudo apt update
  sudo apt install ttyd

Option 2 - Download binary:
  wget https://github.com/tsl0922/ttyd/releases/download/1.7.4/ttyd.x86_64
  sudo mv ttyd.x86_64 /usr/local/bin/ttyd
  sudo chmod +x /usr/local/bin/ttyd

Option 3 - Build from source:
  sudo apt install build-essential cmake git libjson-c-dev libwebsockets-dev
  git clone https://github.com/tsl0922/ttyd.git
  cd ttyd && mkdir build && cd build
  cmake ..
  make && sudo make install

After installation, restart the NovaPanel service.
TEXT;
    }

    // -------------------------------------------------------------------------
    // Internal helpers
    // -------------------------------------------------------------------------

    /**
     * Build the session-info array returned to callers / views.
     */
    private function buildSessionInfo(TerminalSession $session): array
    {
        $config   = @file_exists(__DIR__ . '/../../../config/app.php')
            ? (require __DIR__ . '/../../../config/app.php')
            : [];
        $baseUrl  = $config['url'] ?? 'http://localhost:7080';
        $parts    = parse_url($baseUrl);
        $protocol = $parts['scheme'] ?? 'http';
        $host     = $parts['host']   ?? 'localhost';
        $panelPort = $parts['port']  ?? 7080;

        $url = "{$protocol}://{$host}:{$panelPort}/internal/terminal/{$session->id}";

        return [
            'session_id'  => $session->id,
            'user_id'     => $session->userId,
            'role'        => $session->role,
            'port'        => $session->ttydPort,
            'status'      => $session->status,
            'expires_at'  => $session->expiresAt,
            'last_seen_at' => $session->lastSeenAt,
            'url'         => $url,
        ];
    }

    /**
     * Determine whether a DB session record represents a live, usable session.
     */
    private function isSessionValid(TerminalSession $session): bool
    {
        if ($session->status !== 'active') {
            return false;
        }

        // Check TTL
        if ($session->expiresAt && strtotime($session->expiresAt) <= time()) {
            return false;
        }

        // Check idle timeout
        if ($session->lastSeenAt) {
            $idle = time() - strtotime($session->lastSeenAt);
            if ($idle > self::IDLE_TIMEOUT) {
                return false;
            }
        }

        // Check OS process
        if ($session->processId && !$this->isProcessRunning($session->processId)) {
            return false;
        }

        return true;
    }

    /**
     * Kill the OS process and mark the session as ended in the DB.
     */
    private function terminateSession(TerminalSession $session): void
    {
        if ($session->processId) {
            $this->killProcess($session->processId);
        }
        $this->sessionRepo->markEnded($session->id);
    }

    /**
     * Launch ttyd in the background and return the PID.
     *
     * ttyd options used:
     *   -p {port}       bind to specific port
     *   -m 1            allow only a single WebSocket client
     *   -o              exit once the client disconnects
     *   -W              enable writable mode (required for interactive shells)
     *   -b {base_path}  URL base path (used by the proxy)
     *
     * The wrapper script receives SESSION_ID and ROLE as positional arguments.
     * All arguments are individually shell-escaped; the wrapper path is a
     * hardcoded constant so it cannot be influenced by user input.
     *
     * @throws \RuntimeException if ttyd fails to start
     */
    private function launchTtyd(int $port, string $sessionId, string $role, int $userId): int
    {
        $wrapperExists = file_exists(self::WRAPPER_SCRIPT);
        if ($wrapperExists) {
            // Each component is individually shell-escaped; the wrapper path is
            // a hardcoded constant and cannot be injected externally.
            $shellCmd = escapeshellarg(self::WRAPPER_SCRIPT)
                . ' ' . escapeshellarg($sessionId)
                . ' ' . escapeshellarg($role);
        } else {
            $shellCmd = 'bash -l';
        }

        $logFile  = escapeshellarg($this->logDir . '/' . $userId . '_' . substr($sessionId, 0, 8) . '.log');
        $basePath = escapeshellarg('/internal/terminal/' . $sessionId);

        $command = sprintf(
            'nohup ttyd -p %d -m 1 -o -W -b %s %s > %s 2>&1 & echo $!',
            $port,
            $basePath,
            $shellCmd,
            $logFile
        );

        $output = shell_exec($command);
        $pid    = (int) trim($output ?? '');

        if ($pid <= 0) {
            throw new \RuntimeException(
                'Failed to start terminal session: could not capture process ID'
            );
        }

        // Brief pause to let ttyd bind its port
        usleep(500000);

        if (!$this->isProcessRunning($pid)) {
            $rawLog = $this->logDir . '/' . $userId . '_' . substr($sessionId, 0, 8) . '.log';
            $errorDetails = $this->readLastLines($rawLog, 5);
            throw new \RuntimeException(
                "Terminal process failed to start on port {$port}." .
                ($errorDetails ? " Log: {$errorDetails}" : '')
            );
        }

        return $pid;
    }

    /**
     * Attempt a graceful then forceful process kill.
     */
    private function killProcess(int $pid): void
    {
        if (!$this->isProcessRunning($pid)) {
            return;
        }

        @posix_kill($pid, SIGTERM);
        usleep(500000);

        if ($this->isProcessRunning($pid)) {
            @posix_kill($pid, SIGKILL);
            usleep(500000);
        }

        // Last-resort shell kill
        if ($this->isProcessRunning($pid)) {
            try {
                $this->shell->execute('kill', ['-9', (string) $pid]);
            } catch (\Exception $e) {
                error_log("Shell kill failed for PID {$pid}: " . $e->getMessage());
            }
        }
    }

    /**
     * Check whether a process is running by PID.
     */
    private function isProcessRunning(int $pid): bool
    {
        if ($pid <= 0) {
            return false;
        }

        $result    = posix_kill($pid, 0);
        $errorCode = posix_get_last_error();

        // EPERM (1) = process exists but we lack permission to signal it
        return $result || $errorCode === 1;
    }

    /**
     * Find an available TCP port in the configured range.
     *
     * @throws \RuntimeException if no port is available
     */
    private function findAvailablePort(): int
    {
        for ($port = self::BASE_PORT; $port < self::BASE_PORT + self::PORT_RANGE; $port++) {
            if ($this->isPortAvailable($port)) {
                return $port;
            }
        }

        throw new \RuntimeException(
            'No available ports for terminal session. All ports in range ' .
            self::BASE_PORT . '-' . (self::BASE_PORT + self::PORT_RANGE - 1) . ' are in use.'
        );
    }

    /**
     * Return true when no process is bound to $port on localhost.
     */
    private function isPortAvailable(int $port): bool
    {
        $connection = @fsockopen('127.0.0.1', $port, $errno, $errstr, 1);
        if (is_resource($connection)) {
            fclose($connection);
            return false;
        }
        return true;
    }

    /**
     * Generate a RFC-4122 v4 UUID.
     */
    private function generateUuid(): string
    {
        $data = random_bytes(16);
        $data[6] = chr((ord($data[6]) & 0x0f) | 0x40);
        $data[8] = chr((ord($data[8]) & 0x3f) | 0x80);
        return vsprintf('%s%s-%s-%s-%s-%s%s%s', str_split(bin2hex($data), 4));
    }

    /**
     * Read the last N lines of a log file for error reporting.
     */
    private function readLastLines(string $path, int $lines): string
    {
        if (!file_exists($path)) {
            return '';
        }
        $content = @file_get_contents($path);
        if ($content === false) {
            return '';
        }
        $all = array_filter(explode("\n", trim($content)));
        return implode("\n", array_slice($all, -$lines));
    }
}
