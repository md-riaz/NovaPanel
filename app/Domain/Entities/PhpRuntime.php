<?php

namespace App\Domain\Entities;

class PhpRuntime
{
    public function __construct(
        public ?string $version = null,
        public ?string $binary = null,
        public ?string $fpmSocket = null
    ) {}
}
