<?php

namespace App\Domain\Entities;

class User
{
    public function __construct(
        public ?int $id = null,
        public ?string $username = null,
        public ?string $email = null,
        public ?string $password = null,
        public ?string $createdAt = null,
        public ?string $updatedAt = null
    ) {}
}
