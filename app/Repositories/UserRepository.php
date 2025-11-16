<?php

namespace App\Repositories;

use App\Domain\Entities\User;
use App\Infrastructure\Database;
use PDO;

class UserRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function find(int $id): ?User
    {
        $stmt = $this->db->prepare("SELECT * FROM users WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByUsername(string $username): ?User
    {
        $stmt = $this->db->prepare("SELECT * FROM users WHERE username = ?");
        $stmt->execute([$username]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByEmail(string $email): ?User
    {
        $stmt = $this->db->prepare("SELECT * FROM users WHERE email = ?");
        $stmt->execute([$email]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM users ORDER BY created_at DESC");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function create(User $user): User
    {
        $stmt = $this->db->prepare("
            INSERT INTO users (username, email, password)
            VALUES (?, ?, ?)
        ");
        
        $stmt->execute([
            $user->username,
            $user->email,
            $user->password
        ]);

        $user->id = (int) $this->db->lastInsertId();
        return $user;
    }

    public function update(User $user): bool
    {
        $stmt = $this->db->prepare("
            UPDATE users
            SET username = ?, email = ?, password = ?, updated_at = datetime('now')
            WHERE id = ?
        ");

        return $stmt->execute([
            $user->username,
            $user->email,
            $user->password,
            $user->id
        ]);
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM users WHERE id = ?");
        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): User
    {
        return new User(
            id: (int) $row['id'],
            username: $row['username'],
            email: $row['email'],
            password: $row['password'],
            createdAt: $row['created_at'],
            updatedAt: $row['updated_at']
        );
    }
}
