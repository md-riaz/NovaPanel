<?php

namespace App\Domain\Entities;

class DnsRecord
{
    public function __construct(
        public ?int $id = null,
        public ?int $domainId = null,
        public ?string $name = null,
        public ?string $type = null,
        public ?string $content = null,
        public ?int $ttl = 3600,
        public ?int $priority = null
    ) {}
}
