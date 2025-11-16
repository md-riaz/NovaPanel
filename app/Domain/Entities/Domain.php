<?php

namespace App\Domain\Entities;

class Domain
{
    public function __construct(
        public ?int $id = null,
        public ?int $siteId = null,
        public ?string $name = null,
        public ?string $createdAt = null
    ) {}
}
