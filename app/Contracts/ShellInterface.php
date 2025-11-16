<?php

namespace App\Contracts;

interface ShellInterface
{
    /**
     * Execute a shell command safely
     *
     * @param string $command
     * @param array $args
     * @return array ['output' => string, 'exitCode' => int]
     */
    public function execute(string $command, array $args = []): array;

    /**
     * Execute a command with sudo privileges
     *
     * @param string $command
     * @param array $args
     * @return array ['output' => string, 'exitCode' => int]
     */
    public function executeSudo(string $command, array $args = []): array;

    /**
     * Escape a shell argument
     *
     * @param string $arg
     * @return string
     */
    public function escapeArg(string $arg): string;
}
