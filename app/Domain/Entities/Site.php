<?php

namespace App\Domain\Entities;

class Site
{
    public function __construct(
        public ?int $id = null,
        public ?int $userId = null,
        public ?string $domain = null,
        public ?string $documentRoot = null,
        public ?string $phpVersion = null,
        public ?bool $sslEnabled = false,
        public ?string $createdAt = null,
        public ?string $updatedAt = null,
        public ?string $ownerUsername = null // Non-persistent field for runtime use
    ) {}
}
