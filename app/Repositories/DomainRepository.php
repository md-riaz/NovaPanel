<?php

namespace App\Repositories;

use App\Domain\Entities\Domain;
use App\Infrastructure\Database;
use PDO;

class DomainRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function find(int $id): ?Domain
    {
        $stmt = $this->db->prepare("SELECT * FROM domains WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByName(string $name): ?Domain
    {
        $stmt = $this->db->prepare("SELECT * FROM domains WHERE name = ?");
        $stmt->execute([$name]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM domains ORDER BY created_at DESC");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function findBySiteId(int $siteId): array
    {
        $stmt = $this->db->prepare("SELECT * FROM domains WHERE site_id = ? ORDER BY created_at DESC");
        $stmt->execute([$siteId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function create(Domain $domain): Domain
    {
        $stmt = $this->db->prepare("
            INSERT INTO domains (site_id, name)
            VALUES (?, ?)
        ");
        
        $stmt->execute([
            $domain->siteId,
            $domain->name
        ]);

        $domain->id = (int) $this->db->lastInsertId();
        return $domain;
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM domains WHERE id = ?");
        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): Domain
    {
        return new Domain(
            id: (int) $row['id'],
            siteId: (int) $row['site_id'],
            name: $row['name'],
            createdAt: $row['created_at']
        );
    }
}
