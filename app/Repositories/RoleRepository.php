<?php

namespace App\Repositories;

use App\Domain\Entities\Role;
use App\Infrastructure\Database;
use PDO;

class RoleRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function find(int $id): ?Role
    {
        $stmt = $this->db->prepare("SELECT * FROM roles WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByName(string $name): ?Role
    {
        $stmt = $this->db->prepare("SELECT * FROM roles WHERE name = ?");
        $stmt->execute([$name]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM roles ORDER BY name");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function getUserRoles(int $userId): array
    {
        $stmt = $this->db->prepare("
            SELECT r.* FROM roles r
            INNER JOIN user_roles ur ON r.id = ur.role_id
            WHERE ur.user_id = ?
        ");
        $stmt->execute([$userId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function assignRoleToUser(int $userId, int $roleId): bool
    {
        $stmt = $this->db->prepare("
            INSERT OR IGNORE INTO user_roles (user_id, role_id)
            VALUES (?, ?)
        ");
        
        return $stmt->execute([$userId, $roleId]);
    }

    public function removeRoleFromUser(int $userId, int $roleId): bool
    {
        $stmt = $this->db->prepare("
            DELETE FROM user_roles WHERE user_id = ? AND role_id = ?
        ");
        
        return $stmt->execute([$userId, $roleId]);
    }

    private function hydrate(array $row): Role
    {
        return new Role(
            id: (int) $row['id'],
            name: $row['name'],
            description: $row['description']
        );
    }
}
