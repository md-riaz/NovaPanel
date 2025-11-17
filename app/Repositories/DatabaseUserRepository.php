<?php

namespace App\Repositories;

use App\Domain\Entities\DatabaseUser;
use App\Infrastructure\Database;
use PDO;

class DatabaseUserRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function find(int $id): ?DatabaseUser
    {
        $stmt = $this->db->prepare("SELECT * FROM database_users WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByDatabaseId(int $databaseId): array
    {
        $stmt = $this->db->prepare("SELECT * FROM database_users WHERE database_id = ? ORDER BY created_at DESC");
        $stmt->execute([$databaseId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function create(DatabaseUser $dbUser): DatabaseUser
    {
        $stmt = $this->db->prepare("
            INSERT INTO database_users (database_id, username, host)
            VALUES (?, ?, ?)
        ");
        
        $stmt->execute([
            $dbUser->databaseId,
            $dbUser->username,
            $dbUser->host ?? 'localhost'
        ]);

        $dbUser->id = (int) $this->db->lastInsertId();
        return $dbUser;
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM database_users WHERE id = ?");
        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): DatabaseUser
    {
        return new DatabaseUser(
            id: (int) $row['id'],
            databaseId: (int) $row['database_id'],
            username: $row['username'],
            host: $row['host'],
            createdAt: $row['created_at']
        );
    }
}
