<?php

namespace App\Domain\Entities;

class Role
{
    public function __construct(
        public ?int $id = null,
        public ?string $name = null,
        public ?string $description = null
    ) {}
}
