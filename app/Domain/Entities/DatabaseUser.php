<?php

namespace App\Domain\Entities;

class DatabaseUser
{
    public function __construct(
        public ?int $id = null,
        public ?int $databaseId = null,
        public ?string $username = null,
        public ?string $host = 'localhost',
        public ?string $createdAt = null
    ) {}
}
