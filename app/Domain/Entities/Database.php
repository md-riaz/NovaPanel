<?php

namespace App\Domain\Entities;

class Database
{
    public function __construct(
        public ?int $id = null,
        public ?int $accountId = null,
        public ?string $name = null,
        public ?string $type = 'mysql',
        public ?string $createdAt = null
    ) {}
}
