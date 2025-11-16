<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\DnsManagerInterface;
use App\Domain\Entities\Domain;
use App\Domain\Entities\DnsRecord;
use PDO;

/**
 * PowerDNS Adapter - manages DNS zones and records via PowerDNS MySQL backend
 */
class PowerDnsAdapter implements DnsManagerInterface
{
    private ?PDO $pdnsDb = null;

    public function __construct(
        private string $host = 'localhost',
        private string $database = 'powerdns',
        private string $username = 'powerdns',
        private string $password = ''
    ) {}

    public function createZone(Domain $domain): bool
    {
        $db = $this->getConnection();
        
        try {
            // Create domain in PowerDNS
            $stmt = $db->prepare("
                INSERT INTO domains (name, type, account)
                VALUES (?, 'NATIVE', ?)
            ");
            $stmt->execute([$domain->name, 'novapanel']);
            
            $pdnsDomainId = (int) $db->lastInsertId();
            
            // Add default SOA record
            $this->addDefaultRecords($pdnsDomainId, $domain->name);
            
            return true;
        } catch (\PDOException $e) {
            error_log("PowerDNS zone creation failed: " . $e->getMessage());
            return false;
        }
    }

    public function deleteZone(Domain $domain): bool
    {
        $db = $this->getConnection();
        
        try {
            // Delete all records for this domain
            $stmt = $db->prepare("DELETE FROM records WHERE domain_id IN (SELECT id FROM domains WHERE name = ?)");
            $stmt->execute([$domain->name]);
            
            // Delete domain
            $stmt = $db->prepare("DELETE FROM domains WHERE name = ?");
            $stmt->execute([$domain->name]);
            
            return true;
        } catch (\PDOException $e) {
            error_log("PowerDNS zone deletion failed: " . $e->getMessage());
            return false;
        }
    }

    public function addRecord(DnsRecord $record): bool
    {
        $db = $this->getConnection();
        
        try {
            // Get domain from our records repository
            $domainRepo = new \App\Repositories\DomainRepository();
            $domain = $domainRepo->find($record->domainId);
            
            if (!$domain) {
                throw new \RuntimeException("Domain not found");
            }
            
            // Get PowerDNS domain ID
            $stmt = $db->prepare("SELECT id FROM domains WHERE name = ?");
            $stmt->execute([$domain->name]);
            $pdnsDomainId = $stmt->fetchColumn();
            
            if (!$pdnsDomainId) {
                throw new \RuntimeException("PowerDNS domain not found");
            }
            
            // Insert record
            $stmt = $db->prepare("
                INSERT INTO records (domain_id, name, type, content, ttl, prio)
                VALUES (?, ?, ?, ?, ?, ?)
            ");
            
            $fullName = $record->name === '@' ? $domain->name : $record->name . '.' . $domain->name;
            
            $stmt->execute([
                $pdnsDomainId,
                $fullName,
                $record->type,
                $record->content,
                $record->ttl ?? 3600,
                $record->priority ?? 0
            ]);
            
            return true;
        } catch (\Exception $e) {
            error_log("PowerDNS record creation failed: " . $e->getMessage());
            return false;
        }
    }

    public function updateRecord(DnsRecord $record): bool
    {
        // For PowerDNS, we would need to update the records table
        // This is a simplified implementation
        return $this->deleteRecord($record) && $this->addRecord($record);
    }

    public function deleteRecord(DnsRecord $record): bool
    {
        $db = $this->getConnection();
        
        try {
            $domainRepo = new \App\Repositories\DomainRepository();
            $domain = $domainRepo->find($record->domainId);
            
            if (!$domain) {
                return false;
            }
            
            $stmt = $db->prepare("SELECT id FROM domains WHERE name = ?");
            $stmt->execute([$domain->name]);
            $pdnsDomainId = $stmt->fetchColumn();
            
            if (!$pdnsDomainId) {
                return false;
            }
            
            $fullName = $record->name === '@' ? $domain->name : $record->name . '.' . $domain->name;
            
            $stmt = $db->prepare("
                DELETE FROM records 
                WHERE domain_id = ? AND name = ? AND type = ? AND content = ?
            ");
            $stmt->execute([
                $pdnsDomainId,
                $fullName,
                $record->type,
                $record->content
            ]);
            
            return true;
        } catch (\Exception $e) {
            error_log("PowerDNS record deletion failed: " . $e->getMessage());
            return false;
        }
    }

    private function addDefaultRecords(int $pdnsDomainId, string $domainName): void
    {
        $db = $this->getConnection();
        
        // Add SOA record
        $soa = sprintf(
            'ns1.%s hostmaster.%s 1 10800 3600 604800 3600',
            $domainName,
            $domainName
        );
        
        $stmt = $db->prepare("
            INSERT INTO records (domain_id, name, type, content, ttl, prio)
            VALUES (?, ?, 'SOA', ?, 3600, 0)
        ");
        $stmt->execute([$pdnsDomainId, $domainName, $soa]);
        
        // Add default NS records
        $stmt = $db->prepare("
            INSERT INTO records (domain_id, name, type, content, ttl, prio)
            VALUES (?, ?, 'NS', ?, 3600, 0)
        ");
        $stmt->execute([$pdnsDomainId, $domainName, 'ns1.' . $domainName]);
        $stmt->execute([$pdnsDomainId, $domainName, 'ns2.' . $domainName]);
    }

    private function getConnection(): PDO
    {
        if ($this->pdnsDb === null) {
            try {
                $this->pdnsDb = new PDO(
                    "mysql:host={$this->host};dbname={$this->database}",
                    $this->username,
                    $this->password
                );
                $this->pdnsDb->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
            } catch (\PDOException $e) {
                throw new \RuntimeException("Failed to connect to PowerDNS database: " . $e->getMessage());
            }
        }
        
        return $this->pdnsDb;
    }
}
