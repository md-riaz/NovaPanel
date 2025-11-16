<?php

namespace App\Services;

use App\Domain\Entities\Database;
use App\Domain\Entities\DatabaseUser;
use App\Repositories\DatabaseRepository;
use App\Repositories\DatabaseUserRepository;
use App\Repositories\UserRepository;
use App\Contracts\DatabaseManagerInterface;

class CreateDatabaseService
{
    public function __construct(
        private DatabaseRepository $databaseRepository,
        private DatabaseUserRepository $dbUserRepository,
        private UserRepository $userRepository,
        private DatabaseManagerInterface $databaseManager
    ) {}

    public function execute(
        int $userId,
        string $dbName,
        string $dbType = 'mysql',
        ?string $dbUsername = null,
        ?string $dbPassword = null,
        array $privileges = []
    ): Database {
        // Validate database name
        if (!$this->isValidDatabaseName($dbName)) {
            throw new \InvalidArgumentException('Invalid database name format');
        }

        // Check if database already exists
        if ($this->databaseRepository->findByName($dbName)) {
            throw new \RuntimeException("Database with name '{$dbName}' already exists");
        }

        // Get user
        $user = $this->userRepository->find($userId);
        if (!$user) {
            throw new \RuntimeException("User not found");
        }

        // Create database entity
        $database = new Database(
            userId: $userId,
            name: $dbName,
            type: $dbType
        );

        // Save to panel database
        $database = $this->databaseRepository->create($database);

        try {
            // Create actual database in MySQL/PostgreSQL
            if (!$this->databaseManager->createDatabase($database)) {
                throw new \RuntimeException("Failed to create database in {$dbType} server");
            }

            // Create database user if credentials provided
            if ($dbUsername && $dbPassword) {
                $dbUser = new DatabaseUser(
                    databaseId: $database->id,
                    username: $dbUsername,
                    host: 'localhost'
                );

                // Save to panel database
                $dbUser = $this->dbUserRepository->create($dbUser);

                // Create actual database user
                if (!$this->databaseManager->createUser($dbUser, $dbPassword)) {
                    throw new \RuntimeException("Failed to create database user");
                }

                // Grant privileges
                $privs = $privileges ?: ['ALL PRIVILEGES'];
                if (!$this->databaseManager->grantPrivileges($dbUser, $database, $privs)) {
                    throw new \RuntimeException("Failed to grant privileges");
                }
            }

        } catch (\Exception $e) {
            // Rollback: delete from panel database if infrastructure setup fails
            $this->databaseRepository->delete($database->id);
            throw new \RuntimeException("Failed to create database infrastructure: " . $e->getMessage());
        }

        return $database;
    }

    private function isValidDatabaseName(string $name): bool
    {
        // Database name should be alphanumeric with underscores
        return (bool) preg_match('/^[a-zA-Z0-9_]{1,64}$/', $name);
    }
}
