<?php

namespace App\Services;

use App\Domain\Entities\Domain;
use App\Domain\Entities\DnsRecord;
use App\Repositories\DomainRepository;
use App\Repositories\DnsRecordRepository;
use App\Repositories\SiteRepository;
use App\Contracts\DnsManagerInterface;

class SetupDnsZoneService
{
    public function __construct(
        private DomainRepository $domainRepository,
        private DnsRecordRepository $dnsRecordRepository,
        private SiteRepository $siteRepository,
        private DnsManagerInterface $dnsManager
    ) {}

    public function execute(
        int $siteId,
        string $domainName,
        ?string $serverIp = null
    ): Domain {
        // Validate domain name
        if (!$this->isValidDomain($domainName)) {
            throw new \InvalidArgumentException('Invalid domain name format');
        }

        // Check if domain already exists
        if ($this->domainRepository->findByName($domainName)) {
            throw new \RuntimeException("Domain '{$domainName}' already exists");
        }

        // Verify site exists
        $site = $this->siteRepository->find($siteId);
        if (!$site) {
            throw new \RuntimeException("Site not found");
        }

        // Create domain entity
        $domain = new Domain(
            siteId: $siteId,
            name: $domainName
        );

        // Save to panel database
        $domain = $this->domainRepository->create($domain);

        try {
            // Create DNS zone in BIND9
            if (!$this->dnsManager->createZone($domain)) {
                throw new \RuntimeException("Failed to create DNS zone in BIND9");
            }

            // Add default A record if server IP provided
            if ($serverIp) {
                $this->addDefaultRecords($domain, $serverIp);
            }

        } catch (\Exception $e) {
            // Rollback: delete from panel database if infrastructure setup fails
            $this->domainRepository->delete($domain->id);
            throw new \RuntimeException("Failed to create DNS zone: " . $e->getMessage());
        }

        return $domain;
    }

    private function addDefaultRecords(Domain $domain, string $serverIp): void
    {
        // Add A record for domain
        $aRecord = new DnsRecord(
            domainId: $domain->id,
            name: '@',
            type: 'A',
            content: $serverIp,
            ttl: 3600
        );
        $aRecord = $this->dnsRecordRepository->create($aRecord);
        $this->dnsManager->addRecord($aRecord);

        // Add www CNAME
        $wwwRecord = new DnsRecord(
            domainId: $domain->id,
            name: 'www',
            type: 'CNAME',
            content: $domain->name . '.',
            ttl: 3600
        );
        $wwwRecord = $this->dnsRecordRepository->create($wwwRecord);
        $this->dnsManager->addRecord($wwwRecord);
    }

    private function isValidDomain(string $domain): bool
    {
        return (bool) preg_match('/^[a-z0-9]+([\-\.]{1}[a-z0-9]+)*\.[a-z]{2,}$/', $domain);
    }
}
