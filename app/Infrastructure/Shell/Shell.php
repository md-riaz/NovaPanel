<?php

namespace App\Infrastructure\Shell;

use App\Contracts\ShellInterface;

class Shell implements ShellInterface
{
    private const LOG_FILE = __DIR__ . '/../../../storage/logs/shell.log';
    
    private array $allowedCommands = [
        'nginx',
        'systemctl',
        'useradd',
        'usermod',
        'userdel',
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

    private array $sudoCommands = [
        'systemctl',
        'useradd',
        'usermod',
        'userdel',
        'mkdir',
        'chown',
        'chmod',
        'nginx',
        'cp',
        'ln',
        'rm',
        'pure-pw',
        'bash'
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
        $fullCommand = 'sudo ' . $command . ($escapedArgs ? ' ' . implode(' ', $escapedArgs) : '');
        
        return $this->run($fullCommand);
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
