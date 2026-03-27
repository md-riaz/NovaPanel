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
        $stmt = $this->db->prepare('SELECT * FROM sites WHERE id = ?');
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findWithOwner(int $id): ?Site
    {
        $stmt = $this->db->prepare(
            'SELECT s.*, u.username AS owner_username
            FROM sites s
            LEFT JOIN users u ON u.id = s.user_id
            WHERE s.id = ?'
        );
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findByDomain(string $domain): ?Site
    {
        $stmt = $this->db->prepare('SELECT * FROM sites WHERE domain = ?');
        $stmt->execute([$domain]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function all(): array
    {
        $stmt = $this->db->query('SELECT * FROM sites ORDER BY created_at DESC');
        $rows = $stmt->fetchAll();

        return array_map(fn (array $row) => $this->hydrate($row), $rows);
    }

    public function allWithOwners(): array
    {
        $stmt = $this->db->query(
            'SELECT s.*, u.username AS owner_username
            FROM sites s
            LEFT JOIN users u ON u.id = s.user_id
            ORDER BY s.created_at DESC'
        );

        return array_map(fn (array $row) => $this->hydrate($row), $stmt->fetchAll());
    }

    public function findByUserId(int $userId): array
    {
        $stmt = $this->db->prepare('SELECT * FROM sites WHERE user_id = ? ORDER BY created_at DESC');
        $stmt->execute([$userId]);
        $rows = $stmt->fetchAll();

        return array_map(fn (array $row) => $this->hydrate($row), $rows);
    }

    public function findCertificatesDueForRenewal(int $withinDays = 30): array
    {
        $modifier = sprintf('+%d days', max(0, $withinDays));
        $stmt = $this->db->prepare(
            'SELECT * FROM sites
            WHERE ssl_enabled = 1
              AND certificate_auto_renew = 1
              AND certificate_status IN (\'active\', \'expiring\', \'failed\')
              AND (
                    certificate_expires_at IS NULL
                    OR datetime(certificate_expires_at) <= datetime(\'now\', ?)
                  )
            ORDER BY certificate_expires_at ASC, domain ASC'
        );
        $stmt->execute([$modifier]);

        return array_map(fn (array $row) => $this->hydrate($row), $stmt->fetchAll());
    }

    public function findWithCertificateFailures(): array
    {
        $stmt = $this->db->query(
            "SELECT s.*, u.username AS owner_username
            FROM sites s
            LEFT JOIN users u ON u.id = s.user_id
            WHERE s.last_certificate_error IS NOT NULL
              AND TRIM(s.last_certificate_error) <> ''
            ORDER BY s.updated_at DESC, s.domain ASC"
        );

        return array_map(fn (array $row) => $this->hydrate($row), $stmt->fetchAll());
    }

    public function create(Site $site): Site
    {
        $stmt = $this->db->prepare(
            'INSERT INTO sites (
                user_id,
                domain,
                document_root,
                php_version,
                ssl_enabled,
                certificate_provider,
                certificate_status,
                certificate_expires_at,
                certificate_auto_renew,
                certificate_validation_method,
                certificate_path,
                certificate_key_path,
                force_https,
                last_certificate_renewal_at,
                last_certificate_error
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)'
        );

        $stmt->execute([
            $site->userId,
            $site->domain,
            $site->documentRoot,
            $site->phpVersion,
            $site->sslEnabled ? 1 : 0,
            $site->certificateProvider,
            $site->certificateStatus,
            $site->certificateExpiresAt,
            $site->certificateAutoRenew ? 1 : 0,
            $site->certificateValidationMethod,
            $site->certificatePath,
            $site->certificateKeyPath,
            $site->forceHttps ? 1 : 0,
            $site->lastCertificateRenewalAt,
            $site->lastCertificateError,
        ]);

        $site->id = (int) $this->db->lastInsertId();

        return $site;
    }

    public function update(Site $site): bool
    {
        $stmt = $this->db->prepare(
            'UPDATE sites
            SET domain = ?,
                document_root = ?,
                php_version = ?,
                ssl_enabled = ?,
                certificate_provider = ?,
                certificate_status = ?,
                certificate_expires_at = ?,
                certificate_auto_renew = ?,
                certificate_validation_method = ?,
                certificate_path = ?,
                certificate_key_path = ?,
                force_https = ?,
                last_certificate_renewal_at = ?,
                last_certificate_error = ?,
                updated_at = datetime(\'now\')
            WHERE id = ?'
        );

        return $stmt->execute([
            $site->domain,
            $site->documentRoot,
            $site->phpVersion,
            $site->sslEnabled ? 1 : 0,
            $site->certificateProvider,
            $site->certificateStatus,
            $site->certificateExpiresAt,
            $site->certificateAutoRenew ? 1 : 0,
            $site->certificateValidationMethod,
            $site->certificatePath,
            $site->certificateKeyPath,
            $site->forceHttps ? 1 : 0,
            $site->lastCertificateRenewalAt,
            $site->lastCertificateError,
            $site->id,
        ]);
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare('DELETE FROM sites WHERE id = ?');

        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): Site
    {
        return new Site(
            id: (int) $row['id'],
            userId: (int) $row['user_id'],
            domain: $row['domain'],
            documentRoot: $row['document_root'],
            phpVersion: $row['php_version'],
            sslEnabled: (bool) $row['ssl_enabled'],
            certificateProvider: $row['certificate_provider'] ?? 'letsencrypt',
            certificateStatus: $row['certificate_status'] ?? 'unissued',
            certificateExpiresAt: $row['certificate_expires_at'] ?? null,
            certificateAutoRenew: isset($row['certificate_auto_renew']) ? (bool) $row['certificate_auto_renew'] : true,
            certificateValidationMethod: $row['certificate_validation_method'] ?? 'webroot',
            certificatePath: $row['certificate_path'] ?? null,
            certificateKeyPath: $row['certificate_key_path'] ?? null,
            forceHttps: isset($row['force_https']) ? (bool) $row['force_https'] : false,
            lastCertificateRenewalAt: $row['last_certificate_renewal_at'] ?? null,
            lastCertificateError: $row['last_certificate_error'] ?? null,
            createdAt: $row['created_at'],
            updatedAt: $row['updated_at'],
            ownerUsername: $row['owner_username'] ?? null
        );
    }
}
