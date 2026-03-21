<?php

namespace App\Domain\Entities;

class TerminalSession
{
    public function __construct(
        public ?string $id = null,
        public ?int $userId = null,
        public ?string $role = null,
        public ?int $ttydPort = null,
        public ?int $processId = null,
        public string $status = 'pending',
        public ?string $expiresAt = null,
        public ?string $lastSeenAt = null,
        public ?string $createdAt = null
    ) {}
}
