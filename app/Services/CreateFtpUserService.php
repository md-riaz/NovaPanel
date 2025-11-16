<?php

namespace App\Services;

use App\Domain\Entities\FtpUser;
use App\Repositories\FtpUserRepository;
use App\Repositories\UserRepository;
use App\Contracts\FtpManagerInterface;

class CreateFtpUserService
{
    public function __construct(
        private FtpUserRepository $ftpUserRepository,
        private UserRepository $userRepository,
        private FtpManagerInterface $ftpManager
    ) {}

    public function execute(
        int $userId,
        string $ftpUsername,
        string $password,
        string $homeDirectory
    ): FtpUser {
        // Validate FTP username
        if (!$this->isValidFtpUsername($ftpUsername)) {
            throw new \InvalidArgumentException('Invalid FTP username format');
        }

        // Check if FTP user already exists
        if ($this->ftpUserRepository->findByUsername($ftpUsername)) {
            throw new \RuntimeException("FTP user with username '{$ftpUsername}' already exists");
        }

        // Get panel user
        $user = $this->userRepository->find($userId);
        if (!$user) {
            throw new \RuntimeException("User not found");
        }

        // Validate home directory
        if (!str_starts_with($homeDirectory, '/opt/novapanel/sites/')) {
            throw new \InvalidArgumentException('Home directory must be within /opt/novapanel/sites/');
        }

        // Create FTP user entity
        $ftpUser = new FtpUser(
            userId: $userId,
            username: $ftpUsername,
            homeDirectory: $homeDirectory,
            enabled: true
        );

        // Save to panel database
        $ftpUser = $this->ftpUserRepository->create($ftpUser);

        try {
            // Create actual FTP user via Pure-FTPd
            if (!$this->ftpManager->createUser($ftpUser, $password)) {
                throw new \RuntimeException("Failed to create FTP user in Pure-FTPd");
            }

        } catch (\Exception $e) {
            // Rollback: delete from panel database if infrastructure setup fails
            $this->ftpUserRepository->delete($ftpUser->id);
            throw new \RuntimeException("Failed to create FTP user infrastructure: " . $e->getMessage());
        }

        return $ftpUser;
    }

    private function isValidFtpUsername(string $username): bool
    {
        // FTP username should be alphanumeric with underscores and hyphens
        return (bool) preg_match('/^[a-zA-Z0-9_-]{3,32}$/', $username);
    }
}
