<?php

namespace App\Domain\Entities;

class CronJob
{
    public function __construct(
        public ?int $id = null,
        public ?int $accountId = null,
        public ?string $schedule = null,
        public ?string $command = null,
        public ?bool $enabled = true,
        public ?string $createdAt = null
    ) {}
}
