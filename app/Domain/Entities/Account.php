<?php

namespace App\Domain\Entities;

class Account
{
    public function __construct(
        public ?int $id = null,
        public ?int $userId = null,
        public ?string $username = null,
        public ?string $homeDirectory = null,
        public ?bool $suspended = false,
        public ?string $createdAt = null,
        public ?string $updatedAt = null
    ) {}
}
