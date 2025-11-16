<?php

namespace App\Repositories;

use App\Domain\Entities\Database as DatabaseEntity;
use App\Infrastructure\Database;
use PDO;

class DatabaseRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function count(): int
    {
        $stmt = $this->db->query("SELECT COUNT(*) FROM databases");
        return (int) $stmt->fetchColumn();
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM databases ORDER BY created_at DESC");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function find(int $id): ?DatabaseEntity
    {
        $stmt = $this->db->prepare("SELECT * FROM databases WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByUserId(int $userId): array
    {
        $stmt = $this->db->prepare("SELECT * FROM databases WHERE user_id = ? ORDER BY created_at DESC");
        $stmt->execute([$userId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    private function hydrate(array $row): DatabaseEntity
    {
        return new DatabaseEntity(
            id: (int) $row['id'],
            userId: (int) $row['user_id'],
            name: $row['name'],
            type: $row['type'],
            createdAt: $row['created_at']
        );
    }
}
