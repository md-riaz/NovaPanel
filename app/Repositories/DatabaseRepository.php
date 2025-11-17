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

    public function findByName(string $name): ?DatabaseEntity
    {
        $stmt = $this->db->prepare("SELECT * FROM databases WHERE name = ?");
        $stmt->execute([$name]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function create(DatabaseEntity $database): DatabaseEntity
    {
        $stmt = $this->db->prepare("
            INSERT INTO databases (user_id, name, type)
            VALUES (?, ?, ?)
        ");
        
        $stmt->execute([
            $database->userId,
            $database->name,
            $database->type
        ]);

        $database->id = (int) $this->db->lastInsertId();
        return $database;
    }

    public function update(DatabaseEntity $database): bool
    {
        $stmt = $this->db->prepare("
            UPDATE databases
            SET name = ?, type = ?
            WHERE id = ?
        ");

        return $stmt->execute([
            $database->name,
            $database->type,
            $database->id
        ]);
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM databases WHERE id = ?");
        return $stmt->execute([$id]);
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
