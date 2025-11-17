<?php

namespace App\Infrastructure\Adapters;

use App\Infrastructure\Shell\Shell;

/**
 * Manages ttyd (web terminal) processes for panel users
 * 
 * ttyd is a simple terminal sharing tool that exposes a terminal session via WebSocket.
 * Each user gets their own ttyd instance on a unique port for security isolation.
 */
class TerminalAdapter
{
    private Shell $shell;
    private string $pidDir;
    private string $logDir;
    private int $basePort = 7100;  // Base port for terminal sessions
    
    public function __construct(Shell $shell)
    {
        $this->shell = $shell;
        $this->pidDir = __DIR__ . '/../../../storage/terminal/pids';
        $this->logDir = __DIR__ . '/../../../storage/terminal/logs';
        
        // Ensure directories exist with proper permissions
        if (!is_dir($this->pidDir)) {
            if (!@mkdir($this->pidDir, 0755, true)) {
                error_log("Warning: Failed to create terminal pids directory: {$this->pidDir}");
            }
        }
        if (!is_dir($this->logDir)) {
            if (!@mkdir($this->logDir, 0755, true)) {
                error_log("Warning: Failed to create terminal logs directory: {$this->logDir}");
            }
        }
    }
    
    /**
     * Start a terminal session for a user
     * 
     * @param int $userId The panel user ID
     * @return array ['port' => int, 'token' => string] Session info
     * @throws \RuntimeException If ttyd is not installed or session fails to start
     */
    public function startSession(int $userId): array
    {
        // Check if user already has an active session
        if ($this->isSessionActive($userId)) {
            return $this->getSessionInfo($userId);
        }
        
        // Generate unique port and token for this session
        $port = $this->basePort + $userId;
        $token = bin2hex(random_bytes(16));
        
        // Store session info
        $this->saveSessionInfo($userId, $port, $token);
        
        // Check if ttyd is installed before attempting to start
        if (!$this->isTtydInstalled()) {
            throw new \RuntimeException('ttyd is not installed. Please install ttyd first.');
        }
        
        // Start ttyd process
        // ttyd options:
        // -p: port to listen on
        // -c: credential for basic auth (username:password)
        // -t: terminal type
        // -W: enable writable terminal
        // bash -l: login shell for better environment
        $command = sprintf(
            'nohup ttyd -p %d -c novapanel:%s -t fontSize=14 -W bash -l > %s/%d.log 2>&1 & echo $!',
            $port,
            $token,
            $this->logDir,
            $userId
        );
        
        // Execute command to start ttyd in background
        $output = shell_exec($command);
        $pid = trim($output);
        
        if (empty($pid) || !is_numeric($pid)) {
            throw new \RuntimeException('Failed to start terminal session: could not capture process ID');
        }
        
        // Save PID for later management
        if (@file_put_contents($this->pidDir . '/' . $userId . '.pid', $pid) === false) {
            error_log("Warning: Failed to save terminal PID file for user {$userId}");
        }
        
        // Wait a moment for the process to start
        usleep(500000); // 0.5 seconds
        
        // Verify the process is running
        if (!$this->isProcessRunning($pid)) {
            // Process failed to start - read log for details
            $logFile = $this->logDir . '/' . $userId . '.log';
            $errorDetails = '';
            $specificError = '';
            
            if (file_exists($logFile)) {
                $logContent = @file_get_contents($logFile);
                if ($logContent !== false) {
                    // Check for specific error patterns
                    if (preg_match('/ERROR on binding.*to port (\d+).*\(-1 98\)/', $logContent, $matches)) {
                        $specificError = "Port {$matches[1]} is already in use by another process. ";
                        $specificError .= "Stop the conflicting process or choose a different port.";
                    } elseif (strpos($logContent, 'ERROR on binding') !== false) {
                        $specificError = "Failed to bind to port {$port}. The port may be in use or you may lack permissions.";
                    } elseif (strpos($logContent, 'Permission denied') !== false) {
                        $specificError = "Permission denied. Check if the user has rights to bind to port {$port}.";
                    }
                    
                    // Get last few lines of log for full context
                    $lines = array_filter(explode("\n", trim($logContent)));
                    $errorDetails = implode("\n", array_slice($lines, -5));
                }
            }
            
            $errorMessage = 'Terminal process failed to start. ';
            if (!empty($specificError)) {
                $errorMessage .= $specificError;
            } elseif (!empty($errorDetails)) {
                $errorMessage .= 'Error from log: ' . $errorDetails;
            } else {
                $errorMessage .= 'Check if port ' . $port . ' is already in use or if there are permission issues.';
            }
            
            throw new \RuntimeException($errorMessage);
        }
        
        // Get the base URL from config or construct from request
        $config = require __DIR__ . '/../../../config/app.php';
        $baseUrl = $config['url'] ?? 'http://localhost:7080';
        
        // Parse base URL to get protocol and host
        $urlParts = parse_url($baseUrl);
        $protocol = $urlParts['scheme'] ?? 'http';
        $host = $urlParts['host'] ?? 'localhost';
        $panelPort = $urlParts['port'] ?? 7080;
        
        return [
            'port' => $port,
            'token' => $token,
            'url' => "{$protocol}://{$host}:{$panelPort}/terminal-ws/{$port}"
        ];
    }
    
    /**
     * Stop a terminal session for a user
     * 
     * @param int $userId The panel user ID
     * @return bool True if session was stopped, false if no session existed
     */
    public function stopSession(int $userId): bool
    {
        $pidFile = $this->pidDir . '/' . $userId . '.pid';
        $sessionFile = $this->pidDir . '/' . $userId . '.json';
        
        if (!file_exists($pidFile)) {
            return false;
        }
        
        $pid = trim(file_get_contents($pidFile));
        
        // Kill the process
        if ($this->isProcessRunning($pid)) {
            posix_kill((int)$pid, SIGTERM);
            
            // Wait a moment, then force kill if still running
            sleep(1);
            if ($this->isProcessRunning($pid)) {
                posix_kill((int)$pid, SIGKILL);
            }
        }
        
        // Clean up files
        @unlink($pidFile);
        @unlink($sessionFile);
        
        return true;
    }
    
    /**
     * Check if a user has an active terminal session
     * 
     * @param int $userId The panel user ID
     * @return bool
     */
    public function isSessionActive(int $userId): bool
    {
        $pidFile = $this->pidDir . '/' . $userId . '.pid';
        
        if (!file_exists($pidFile)) {
            return false;
        }
        
        $pid = trim(file_get_contents($pidFile));
        return $this->isProcessRunning($pid);
    }
    
    /**
     * Get session information for a user
     * 
     * @param int $userId The panel user ID
     * @return array|null Session info or null if no active session
     */
    public function getSessionInfo(int $userId): ?array
    {
        $sessionFile = $this->pidDir . '/' . $userId . '.json';
        
        if (!file_exists($sessionFile)) {
            return null;
        }
        
        $info = json_decode(file_get_contents($sessionFile), true);
        
        // Add URL for convenience
        if ($info) {
            $info['url'] = "http://localhost:{$info['port']}";
        }
        
        return $info;
    }
    
    /**
     * Stop all terminal sessions
     * Useful for maintenance or shutdown
     * 
     * @return int Number of sessions stopped
     */
    public function stopAllSessions(): int
    {
        $count = 0;
        $files = glob($this->pidDir . '/*.pid');
        
        foreach ($files as $file) {
            $userId = (int) basename($file, '.pid');
            if ($this->stopSession($userId)) {
                $count++;
            }
        }
        
        return $count;
    }
    
    /**
     * Get list of all active sessions
     * 
     * @return array Array of user IDs with active sessions
     */
    public function getActiveSessions(): array
    {
        $active = [];
        $files = glob($this->pidDir . '/*.pid');
        
        foreach ($files as $file) {
            $userId = (int) basename($file, '.pid');
            if ($this->isSessionActive($userId)) {
                $active[] = $userId;
            }
        }
        
        return $active;
    }
    
    /**
     * Check if ttyd is installed on the system
     * 
     * @return bool
     */
    public function isTtydInstalled(): bool
    {
        $result = shell_exec('which ttyd 2>/dev/null');
        return !empty(trim($result));
    }
    
    /**
     * Get ttyd installation instructions
     * 
     * @return string Installation instructions for ttyd
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
    
    /**
     * Save session information to file
     */
    private function saveSessionInfo(int $userId, int $port, string $token): void
    {
        $info = [
            'user_id' => $userId,
            'port' => $port,
            'token' => $token,
            'created_at' => time()
        ];
        
        if (@file_put_contents(
            $this->pidDir . '/' . $userId . '.json',
            json_encode($info, JSON_PRETTY_PRINT)
        ) === false) {
            error_log("Warning: Failed to save terminal session info for user {$userId}");
        }
    }
    
    /**
     * Check if a process is running by PID
     * 
     * Uses posix_kill with signal 0 to check process existence.
     * Note: Returns true if process exists, even if we don't have permission to signal it.
     */
    private function isProcessRunning(string $pid): bool
    {
        if (empty($pid) || !is_numeric($pid)) {
            return false;
        }
        
        // Use posix_kill with signal 0 to check if process exists
        $result = posix_kill((int)$pid, 0);
        
        if ($result) {
            // Process exists and we can signal it
            return true;
        }
        
        // Check the error code to determine if process exists
        $errorCode = posix_get_last_error();
        
        // EPERM (1) means the process exists but we don't have permission to signal it
        // This can happen with processes started via nohup or running under different permissions
        // ESRCH (3) means the process doesn't exist
        if ($errorCode === 1) { // EPERM - Operation not permitted
            return true; // Process exists, just no permission to signal it
        }
        
        // For any other error (including ESRCH), treat as not running
        return false;
    }
}
