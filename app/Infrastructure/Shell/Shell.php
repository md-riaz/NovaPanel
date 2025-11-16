<?php

namespace App\Infrastructure\Shell;

use App\Contracts\ShellInterface;

class Shell implements ShellInterface
{
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
        'pdns_control'
    ];

    private array $sudoCommands = [
        'systemctl',
        'useradd',
        'usermod',
        'userdel',
        'mkdir',
        'chown',
        'chmod',
        'nginx'
    ];

    public function execute(string $command, array $args = []): array
    {
        $this->validateCommand($command);
        
        $escapedArgs = array_map(fn($arg) => $this->escapeArg($arg), $args);
        $fullCommand = $command . ' ' . implode(' ', $escapedArgs);
        
        return $this->run($fullCommand);
    }

    public function executeSudo(string $command, array $args = []): array
    {
        $this->validateCommand($command);
        $this->validateSudoCommand($command);
        
        $escapedArgs = array_map(fn($arg) => $this->escapeArg($arg), $args);
        $fullCommand = 'sudo ' . $command . ' ' . implode(' ', $escapedArgs);
        
        return $this->run($fullCommand);
    }

    public function escapeArg(string $arg): string
    {
        return escapeshellarg($arg);
    }

    private function validateCommand(string $command): void
    {
        $baseCommand = explode(' ', $command)[0];
        
        if (!in_array($baseCommand, $this->allowedCommands)) {
            throw new \RuntimeException("Command '$baseCommand' is not allowed for security reasons");
        }
    }

    private function validateSudoCommand(string $command): void
    {
        $baseCommand = explode(' ', $command)[0];
        
        if (!in_array($baseCommand, $this->sudoCommands)) {
            throw new \RuntimeException("Command '$baseCommand' is not allowed to run with sudo");
        }
    }

    private function run(string $command): array
    {
        $output = [];
        $exitCode = 0;
        
        exec($command . ' 2>&1', $output, $exitCode);
        
        return [
            'output' => implode("\n", $output),
            'exitCode' => $exitCode
        ];
    }
}
