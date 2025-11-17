<?php

namespace App\Repositories;

use App\Domain\Entities\CronJob;
use App\Infrastructure\Database;
use PDO;

class CronJobRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
    }

    public function find(int $id): ?CronJob
    {
        $stmt = $this->db->prepare("SELECT * FROM cron_jobs WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function all(): array
    {
        $stmt = $this->db->query("SELECT * FROM cron_jobs ORDER BY created_at DESC");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function findByUserId(int $userId): array
    {
        $stmt = $this->db->prepare("SELECT * FROM cron_jobs WHERE user_id = ? ORDER BY created_at DESC");
        $stmt->execute([$userId]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function create(CronJob $cronJob): CronJob
    {
        $stmt = $this->db->prepare("
            INSERT INTO cron_jobs (user_id, schedule, command, enabled)
            VALUES (?, ?, ?, ?)
        ");
        
        $stmt->execute([
            $cronJob->userId,
            $cronJob->schedule,
            $cronJob->command,
            $cronJob->enabled ? 1 : 0
        ]);

        $cronJob->id = (int) $this->db->lastInsertId();
        return $cronJob;
    }

    public function update(CronJob $cronJob): bool
    {
        $stmt = $this->db->prepare("
            UPDATE cron_jobs
            SET schedule = ?, command = ?, enabled = ?
            WHERE id = ?
        ");

        return $stmt->execute([
            $cronJob->schedule,
            $cronJob->command,
            $cronJob->enabled ? 1 : 0,
            $cronJob->id
        ]);
    }

    public function delete(int $id): bool
    {
        $stmt = $this->db->prepare("DELETE FROM cron_jobs WHERE id = ?");
        return $stmt->execute([$id]);
    }

    private function hydrate(array $row): CronJob
    {
        return new CronJob(
            id: (int) $row['id'],
            userId: (int) $row['user_id'],
            schedule: $row['schedule'],
            command: $row['command'],
            enabled: (bool) $row['enabled'],
            createdAt: $row['created_at']
        );
    }
}
