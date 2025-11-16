<?php

namespace App\Services;

use App\Domain\Entities\Account;
use App\Repositories\AccountRepository;
use App\Contracts\ShellInterface;

class CreateAccountService
{
    public function __construct(
        private AccountRepository $accountRepository,
        private ShellInterface $shell
    ) {}

    public function execute(int $userId, string $username, ?string $homeDirectory = null): Account
    {
        // Validate username
        if (!preg_match('/^[a-z][a-z0-9_-]{2,31}$/', $username)) {
            throw new \InvalidArgumentException('Invalid username format');
        }

        // Check if account already exists
        if ($this->accountRepository->findByUsername($username)) {
            throw new \RuntimeException("Account with username '$username' already exists");
        }

        // Set default home directory if not provided
        if ($homeDirectory === null) {
            $homeDirectory = "/home/{$username}";
        }

        // Create system user
        $result = $this->shell->executeSudo('useradd', [
            '-m',
            '-d', $homeDirectory,
            '-s', '/bin/bash',
            $username
        ]);

        if ($result['exitCode'] !== 0) {
            throw new \RuntimeException("Failed to create system user: " . $result['output']);
        }

        // Create directory structure
        $this->createDirectoryStructure($homeDirectory);

        // Set proper permissions
        $this->shell->executeSudo('chown', ['-R', "{$username}:{$username}", $homeDirectory]);
        $this->shell->executeSudo('chmod', ['755', $homeDirectory]);

        // Create account in database
        $account = new Account(
            userId: $userId,
            username: $username,
            homeDirectory: $homeDirectory,
            suspended: false
        );

        return $this->accountRepository->create($account);
    }

    private function createDirectoryStructure(string $homeDirectory): void
    {
        $directories = [
            'public_html',
            'logs',
            'tmp',
            'backups'
        ];

        foreach ($directories as $dir) {
            $this->shell->executeSudo('mkdir', ['-p', "{$homeDirectory}/{$dir}"]);
        }
    }
}
