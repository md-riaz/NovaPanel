<?php

namespace App\Repositories;

use App\Domain\Entities\Site;
use App\Infrastructure\Database;
use PDO;

class SiteRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function find(int $id): ?Site
    {
        $stmt = $this->db->prepare("SELECT * FROM sites WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByDomain(string $domain): ?Site
    {
        $stmt = $this->db->prepare("SELECT * FROM sites WHERE domain = ?");
        $stmt->execute([$domain]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM sites ORDER BY created_at DESC");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function findByAccountId(int $accountId): array
    {
        $stmt = $this->db->prepare("SELECT * FROM sites WHERE account_id = ? ORDER BY created_at DESC");
        $stmt->execute([$accountId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function create(Site $site): Site
    {
        $stmt = $this->db->prepare("
            INSERT INTO sites (account_id, domain, document_root, php_version, ssl_enabled)
            VALUES (?, ?, ?, ?, ?)
        ");
        
        $stmt->execute([
            $site->accountId,
            $site->domain,
            $site->documentRoot,
            $site->phpVersion,
            $site->sslEnabled ? 1 : 0
        ]);

        $site->id = (int) $this->db->lastInsertId();
        return $site;
    }

    public function update(Site $site): bool
    {
        $stmt = $this->db->prepare("
            UPDATE sites
            SET domain = ?, document_root = ?, php_version = ?, ssl_enabled = ?, updated_at = datetime('now')
            WHERE id = ?
        ");

        return $stmt->execute([
            $site->domain,
            $site->documentRoot,
            $site->phpVersion,
            $site->sslEnabled ? 1 : 0,
            $site->id
        ]);
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM sites WHERE id = ?");
        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): Site
    {
        return new Site(
            id: (int) $row['id'],
            accountId: (int) $row['account_id'],
            domain: $row['domain'],
            documentRoot: $row['document_root'],
            phpVersion: $row['php_version'],
            sslEnabled: (bool) $row['ssl_enabled'],
            createdAt: $row['created_at'],
            updatedAt: $row['updated_at']
        );
    }
}
