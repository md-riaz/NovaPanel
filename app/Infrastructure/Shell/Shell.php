<?php

namespace App\Infrastructure\Shell;

use App\Contracts\ShellInterface;

class Shell implements ShellInterface
{
    private const LOG_FILE = __DIR__ . '/../../../storage/logs/shell.log';
    
    // Single VPS Model: All operations run under the 'novapanel' Linux user
    // No Linux user creation/modification/deletion commands are allowed
    private array $allowedCommands = [
        'nginx',
        'systemctl',
        'mkdir',
        'chown',
        'chmod',
        'ln',
        'rm',
        'cp',
        'mv',
        'cat',
        'touch',
        'crontab',
        'mysql',
        'psql',
        'pure-pw',
        'pdns_control',
        'id',
        'bash'
    ];

    // Commands that are allowed to run with sudo privileges
    // Note: No Linux user management commands (useradd/usermod/userdel) are allowed
    private array $sudoCommands = [
        'systemctl',
        'mkdir',
        'chown',
        'chmod',
        'nginx',
        'cp',
        'mv',
        'ln',
        'rm',
        'pure-pw',
        'bash',
        'crontab'
    ];

    public function execute(string $command, array $args = []): array
    {
        $this->validateCommand($command);
        
        $escapedArgs = array_map(fn($arg) => $this->escapeArg($arg), $args);
        $fullCommand = $command . ($escapedArgs ? ' ' . implode(' ', $escapedArgs) : '');
        
        return $this->run($fullCommand);
    }

    public function executeSudo(string $command, array $args = []): array
    {
        $this->validateCommand($command);
        $this->validateSudoCommand($command);
        
        $escapedArgs = array_map(fn($arg) => $this->escapeArg($arg), $args);
        $fullCommand = 'sudo -n ' . $command . ($escapedArgs ? ' ' . implode(' ', $escapedArgs) : '');
        
        $result = $this->run($fullCommand);
        
        // Check if sudo failed due to password requirement
        // sudo -n returns exit code 1 and outputs various error messages when password is required
        // We only check for password errors, not other sudo failures (like command not allowed)
        if ($result['exitCode'] === 1) {
            $output = strtolower($result['output']);
            
            // These patterns specifically indicate sudo password requirement or NOPASSWD configuration issue
            // We check for sudo-specific patterns to avoid false positives from command output
            $passwordErrors = [
                'sudo: a password is required',
                'sudo: no password was provided',
                'sorry, a password is required',
                'a password is required to run sudo',
                'password is required',  // Generic fallback
            ];
            
            // Only trigger if the output looks like it came from sudo itself
            $sudoIndicators = ['sudo:', 'sorry,', 'password'];
            $hasSudoIndicator = false;
            foreach ($sudoIndicators as $indicator) {
                if (str_contains($output, $indicator)) {
                    $hasSudoIndicator = true;
                    break;
                }
            }
            
            if ($hasSudoIndicator) {
                foreach ($passwordErrors as $errorPattern) {
                    if (str_contains($output, $errorPattern)) {
                        // Try to provide a helpful solution
                        $panelDir = dirname(__DIR__, 3);
                        $setupScript = "{$panelDir}/scripts/setup-sudoers.sh";
                        
                        $message = "Sudo configuration is missing or incomplete. NovaPanel requires NOPASSWD sudo access.\n\n";
                        $message .= "Detected error: " . trim($result['output']) . "\n\n";
                        
                        if (file_exists($setupScript)) {
                            $message .= "🔧 QUICK FIX: Run this command to automatically configure sudo:\n";
                            $message .= "   sudo bash {$setupScript}\n\n";
                        }
                        
                        $message .= "Or manually configure /etc/sudoers.d/novapanel as documented in SECURITY.md:\n";
                        $message .= "   sudo visudo -f /etc/sudoers.d/novapanel\n\n";
                        $message .= "If you haven't installed NovaPanel yet, please run the installation script:\n";
                        $message .= "   sudo bash {$panelDir}/install.sh";
                        
                        throw new \RuntimeException($message);
                    }
                }
            }
        }
        
        return $result;
    }

    public function escapeArg(string $arg): string
    {
        return escapeshellarg($arg);
    }
    
    /**
     * Write content to a file using sudo privileges
     */
    public function writeFile(string $path, string $content): array
    {
        $tempFile = tempnam(sys_get_temp_dir(), 'novapanel_');
        file_put_contents($tempFile, $content);
        
        // Use sudo to move temp file to destination with proper permissions
        $result = $this->executeSudo('cp', [$tempFile, $path]);
        
        // Set proper permissions
        if ($result['exitCode'] === 0) {
            $this->executeSudo('chmod', ['644', $path]);
        }
        
        // Clean up temp file
        @unlink($tempFile);
        
        return $result;
    }

    private function validateCommand(string $command): void
    {
        // Reject commands containing whitespace or shell metacharacters
        if (preg_match('/[\s;|&$`<>(){}[\]\\\\]/', $command)) {
            throw new \RuntimeException("Command contains invalid characters. Use the args parameter instead.");
        }
        
        if (!in_array($command, $this->allowedCommands)) {
            throw new \RuntimeException("Command '$command' is not allowed for security reasons");
        }
    }

    private function validateSudoCommand(string $command): void
    {
        if (!in_array($command, $this->sudoCommands)) {
            throw new \RuntimeException("Command '$command' is not allowed to run with sudo");
        }
    }

    private function run(string $command): array
    {
        $output = [];
        $exitCode = 0;
        
        // Log command execution for audit trail
        $this->logCommand($command);
        
        exec($command . ' 2>&1', $output, $exitCode);
        
        // Log result
        $this->logResult($command, $exitCode);
        
        return [
            'output' => implode("\n", $output),
            'exitCode' => $exitCode
        ];
    }
    
    private function logCommand(string $command): void
    {
        $timestamp = date('Y-m-d H:i:s');
        $user = posix_getpwuid(posix_geteuid())['name'] ?? 'unknown';
        $logMessage = sprintf(
            "[%s] USER=%s COMMAND=%s\n",
            $timestamp,
            $user,
            $command
        );
        
        @file_put_contents(self::LOG_FILE, $logMessage, FILE_APPEND);
    }
    
    private function logResult(string $command, int $exitCode): void
    {
        if ($exitCode !== 0) {
            $timestamp = date('Y-m-d H:i:s');
            $logMessage = sprintf(
                "[%s] FAILED COMMAND=%s EXIT_CODE=%d\n",
                $timestamp,
                $command,
                $exitCode
            );
            
            @file_put_contents(self::LOG_FILE, $logMessage, FILE_APPEND);
        }
    }
}
