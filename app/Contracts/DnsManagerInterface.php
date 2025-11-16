<?php

namespace App\Contracts;

use App\Domain\Entities\Domain;
use App\Domain\Entities\DnsRecord;

interface DnsManagerInterface
{
    /**
     * Create a new DNS zone for a domain
     *
     * @param Domain $domain
     * @return bool
     */
    public function createZone(Domain $domain): bool;

    /**
     * Delete a DNS zone for a domain
     *
     * @param Domain $domain
     * @return bool
     */
    public function deleteZone(Domain $domain): bool;

    /**
     * Add a DNS record to a zone
     *
     * @param DnsRecord $record
     * @return bool
     */
    public function addRecord(DnsRecord $record): bool;

    /**
     * Update a DNS record
     *
     * @param DnsRecord $record
     * @return bool
     */
    public function updateRecord(DnsRecord $record): bool;

    /**
     * Delete a DNS record
     *
     * @param DnsRecord $record
     * @return bool
     */
    public function deleteRecord(DnsRecord $record): bool;
}
