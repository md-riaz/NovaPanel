<?php

namespace App\Repositories;

use App\Domain\Entities\DnsRecord;
use App\Infrastructure\Database;
use PDO;

class DnsRecordRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function find(int $id): ?DnsRecord
    {
        $stmt = $this->db->prepare("SELECT * FROM dns_records WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM dns_records ORDER BY created_at DESC");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function findByDomainId(int $domainId): array
    {
        $stmt = $this->db->prepare("SELECT * FROM dns_records WHERE domain_id = ? ORDER BY type, name");
        $stmt->execute([$domainId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function create(DnsRecord $record): DnsRecord
    {
        $stmt = $this->db->prepare("
            INSERT INTO dns_records (domain_id, name, type, content, ttl, priority)
            VALUES (?, ?, ?, ?, ?, ?)
        ");
        
        $stmt->execute([
            $record->domainId,
            $record->name,
            $record->type,
            $record->content,
            $record->ttl ?? 3600,
            $record->priority
        ]);

        $record->id = (int) $this->db->lastInsertId();
        return $record;
    }

    public function update(DnsRecord $record): bool
    {
        $stmt = $this->db->prepare("
            UPDATE dns_records
            SET name = ?, type = ?, content = ?, ttl = ?, priority = ?
            WHERE id = ?
        ");

        return $stmt->execute([
            $record->name,
            $record->type,
            $record->content,
            $record->ttl,
            $record->priority,
            $record->id
        ]);
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM dns_records WHERE id = ?");
        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): DnsRecord
    {
        return new DnsRecord(
            id: (int) $row['id'],
            domainId: (int) $row['domain_id'],
            name: $row['name'],
            type: $row['type'],
            content: $row['content'],
            ttl: (int) $row['ttl'],
            priority: $row['priority'] ? (int) $row['priority'] : null,
            createdAt: $row['created_at']
        );
    }
}
