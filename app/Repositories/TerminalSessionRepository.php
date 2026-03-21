<?php

namespace App\Repositories;

use App\Domain\Entities\TerminalSession;
use App\Infrastructure\Database;
use PDO;

class TerminalSessionRepository
{
    private PDO $db;

    public function __construct()
    {
        $this->db = Database::panel();
        $this->ensureTableExists();
    }

    public function find(string $id): ?TerminalSession
    {
        $stmt = $this->db->prepare("SELECT * FROM terminal_sessions WHERE id = ?");
        $stmt->execute([$id]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findActiveByUserId(int $userId): ?TerminalSession
    {
        $stmt = $this->db->prepare("
            SELECT * FROM terminal_sessions
            WHERE user_id = ? AND status = 'active'
            ORDER BY created_at DESC
            LIMIT 1
        ");
        $stmt->execute([$userId]);
        $row = $stmt->fetch();

        return $row ? $this->hydrate($row) : null;
    }

    public function findAllActive(): array
    {
        $stmt = $this->db->query("
            SELECT * FROM terminal_sessions WHERE status = 'active' ORDER BY created_at DESC
        ");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function findExpired(): array
    {
        $stmt = $this->db->query("
            SELECT * FROM terminal_sessions
            WHERE status = 'active' AND expires_at <= datetime('now')
        ");
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function findIdleSince(int $seconds): array
    {
        $threshold = date('Y-m-d H:i:s', time() - $seconds);
        $stmt = $this->db->prepare("
            SELECT * FROM terminal_sessions
            WHERE status = 'active' AND last_seen_at <= ?
        ");
        $stmt->execute([$threshold]);
        $rows = $stmt->fetchAll();

        return array_map(fn($row) => $this->hydrate($row), $rows);
    }

    public function create(TerminalSession $session): TerminalSession
    {
        $stmt = $this->db->prepare("
            INSERT INTO terminal_sessions
                (id, user_id, role, ttyd_port, process_id, status, expires_at, last_seen_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ");

        $stmt->execute([
            $session->id,
            $session->userId,
            $session->role,
            $session->ttydPort,
            $session->processId,
            $session->status,
            $session->expiresAt,
            $session->lastSeenAt,
        ]);

        $session->createdAt = date('Y-m-d H:i:s');
        return $session;
    }

    public function update(TerminalSession $session): bool
    {
        $stmt = $this->db->prepare("
            UPDATE terminal_sessions
            SET ttyd_port = ?, process_id = ?, status = ?, expires_at = ?, last_seen_at = ?
            WHERE id = ?
        ");

        return $stmt->execute([
            $session->ttydPort,
            $session->processId,
            $session->status,
            $session->expiresAt,
            $session->lastSeenAt,
            $session->id,
        ]);
    }

    public function markEnded(string $id): bool
    {
        $stmt = $this->db->prepare("
            UPDATE terminal_sessions SET status = 'ended' WHERE id = ?
        ");
        return $stmt->execute([$id]);
    }

    public function updateLastSeen(string $id): bool
    {
        $stmt = $this->db->prepare("
            UPDATE terminal_sessions SET last_seen_at = datetime('now') WHERE id = ?
        ");
        return $stmt->execute([$id]);
    }

    public function deleteEnded(int $olderThanDays = 7): int
    {
        $threshold = date('Y-m-d H:i:s', strtotime("-{$olderThanDays} days"));
        $stmt = $this->db->prepare("
            DELETE FROM terminal_sessions
            WHERE status = 'ended' AND created_at <= ?
        ");
        $stmt->execute([$threshold]);
        return $stmt->rowCount();
    }

    private function hydrate(array $row): TerminalSession
    {
        return new TerminalSession(
            id: $row['id'],
            userId: (int) $row['user_id'],
            role: $row['role'],
            ttydPort: $row['ttyd_port'] !== null ? (int) $row['ttyd_port'] : null,
            processId: $row['process_id'] !== null ? (int) $row['process_id'] : null,
            status: $row['status'],
            expiresAt: $row['expires_at'],
            lastSeenAt: $row['last_seen_at'],
            createdAt: $row['created_at'],
        );
    }

    private function ensureTableExists(): void
    {
        $this->db->exec("
            CREATE TABLE IF NOT EXISTS terminal_sessions (
                id TEXT PRIMARY KEY,
                user_id INTEGER NOT NULL,
                role TEXT NOT NULL,
                ttyd_port INTEGER,
                process_id INTEGER,
                status TEXT NOT NULL DEFAULT 'pending',
                expires_at TEXT NOT NULL,
                last_seen_at TEXT NOT NULL,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )
        ");
    }
}
