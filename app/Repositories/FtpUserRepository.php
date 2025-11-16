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
