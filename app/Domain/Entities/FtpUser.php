<?php

namespace App\Domain\Entities;

class FtpUser
{
    public function __construct(
        public ?int $id = null,
        public ?int $accountId = null,
        public ?string $username = null,
        public ?string $homeDirectory = null,
        public ?bool $enabled = true,
        public ?string $createdAt = null
    ) {}
}
