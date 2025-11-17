<?php

namespace App\Repositories;

use App\Domain\Entities\FtpUser;
use App\Infrastructure\Database;
use PDO;

class FtpUserRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function count(): int
    {
        $stmt = $this->db->query("SELECT COUNT(*) FROM ftp_users");
        return (int) $stmt->fetchColumn();
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM ftp_users ORDER BY created_at DESC");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function find(int $id): ?FtpUser
    {
        $stmt = $this->db->prepare("SELECT * FROM ftp_users WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByUserId(int $userId): array
    {
        $stmt = $this->db->prepare("SELECT * FROM ftp_users WHERE user_id = ? ORDER BY created_at DESC");
        $stmt->execute([$userId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function findByUsername(string $username): ?FtpUser
    {
        $stmt = $this->db->prepare("SELECT * FROM ftp_users WHERE username = ?");
        $stmt->execute([$username]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function create(FtpUser $ftpUser): FtpUser
    {
        $stmt = $this->db->prepare("
            INSERT INTO ftp_users (user_id, username, home_directory, enabled)
            VALUES (?, ?, ?, ?)
        ");
        
        $stmt->execute([
            $ftpUser->userId,
            $ftpUser->username,
            $ftpUser->homeDirectory,
            $ftpUser->enabled ? 1 : 0
        ]);

        $ftpUser->id = (int) $this->db->lastInsertId();
        return $ftpUser;
    }

    public function update(FtpUser $ftpUser): bool
    {
        $stmt = $this->db->prepare("
            UPDATE ftp_users
            SET username = ?, home_directory = ?, enabled = ?
            WHERE id = ?
        ");

        return $stmt->execute([
            $ftpUser->username,
            $ftpUser->homeDirectory,
            $ftpUser->enabled ? 1 : 0,
            $ftpUser->id
        ]);
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM ftp_users WHERE id = ?");
        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): FtpUser
    {
        return new FtpUser(
            id: (int) $row['id'],
            userId: (int) $row['user_id'],
            username: $row['username'],
            homeDirectory: $row['home_directory'],
            enabled: (bool) $row['enabled'],
            createdAt: $row['created_at']
        );
    }
}
