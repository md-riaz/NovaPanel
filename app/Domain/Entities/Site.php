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
        public ?string $certificateProvider = 'letsencrypt',
        public ?string $certificateStatus = 'unissued',
        public ?string $certificateExpiresAt = null,
        public ?bool $certificateAutoRenew = true,
        public ?string $certificateValidationMethod = 'webroot',
        public ?string $certificatePath = null,
        public ?string $certificateKeyPath = null,
        public ?bool $forceHttps = false,
        public ?string $lastCertificateRenewalAt = null,
        public ?string $lastCertificateError = null,
        public ?string $createdAt = null,
        public ?string $updatedAt = null,
        public ?string $ownerUsername = null
    ) {}

    public function hasActiveCertificate(): bool
    {
        return $this->sslEnabled === true
            && $this->certificateStatus === 'active'
            && !empty($this->certificatePath)
            && !empty($this->certificateKeyPath);
    }
}
