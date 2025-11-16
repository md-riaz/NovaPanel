<?php

namespace App\Repositories;

use App\Domain\Entities\Account;
use App\Infrastructure\Database;
use PDO;

class AccountRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function find(int $id): ?Account
    {
        $stmt = $this->db->prepare("SELECT * FROM accounts WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByUsername(string $username): ?Account
    {
        $stmt = $this->db->prepare("SELECT * FROM accounts WHERE username = ?");
        $stmt->execute([$username]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM accounts ORDER BY created_at DESC");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function findByUserId(int $userId): array
    {
        $stmt = $this->db->prepare("SELECT * FROM accounts WHERE user_id = ? ORDER BY created_at DESC");
        $stmt->execute([$userId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function create(Account $account): Account
    {
        $stmt = $this->db->prepare("
            INSERT INTO accounts (user_id, username, home_directory, suspended)
            VALUES (?, ?, ?, ?)
        ");
        
        $stmt->execute([
            $account->userId,
            $account->username,
            $account->homeDirectory,
            $account->suspended ? 1 : 0
        ]);

        $account->id = (int) $this->db->lastInsertId();
        return $account;
    }

    public function update(Account $account): bool
    {
        $stmt = $this->db->prepare("
            UPDATE accounts
            SET username = ?, home_directory = ?, suspended = ?, updated_at = datetime('now')
            WHERE id = ?
        ");

        return $stmt->execute([
            $account->username,
            $account->homeDirectory,
            $account->suspended ? 1 : 0,
            $account->id
        ]);
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM accounts WHERE id = ?");
        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): Account
    {
        return new Account(
            id: (int) $row['id'],
            userId: (int) $row['user_id'],
            username: $row['username'],
            homeDirectory: $row['home_directory'],
            suspended: (bool) $row['suspended'],
            createdAt: $row['created_at'],
            updatedAt: $row['updated_at']
        );
    }
}
