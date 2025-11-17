<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\DnsManagerInterface;
use App\Contracts\ShellInterface;
use App\Domain\Entities\Domain;
use App\Domain\Entities\DnsRecord;

/**
 * BIND9 Adapter - manages DNS zones and records via zone files
 * Provides complete isolation from database access
 */
class BindAdapter implements DnsManagerInterface
{
    private string $zonesPath;
    private string $namedConfPath;
    
    public function __construct(
        private ShellInterface $shell,
        string $zonesPath = '/etc/bind/zones',
        string $namedConfPath = '/etc/bind/named.conf.local'
    ) {
        $this->zonesPath = $zonesPath;
        $this->namedConfPath = $namedConfPath;
    }

    public function createZone(Domain $domain): bool
    {
        try {
            // Ensure zones directory exists
            if (!is_dir($this->zonesPath)) {
                $this->shell->exec("sudo mkdir -p {$this->zonesPath}");
                $this->shell->exec("sudo chown bind:bind {$this->zonesPath}");
                $this->shell->exec("sudo chmod 755 {$this->zonesPath}");
            }
            
            $zoneFile = $this->getZoneFilePath($domain->name);
            
            // Check if zone file already exists
            if (file_exists($zoneFile)) {
                error_log("BIND9: Zone file already exists for {$domain->name}");
                return false;
            }
            
            // Generate initial zone file content
            $zoneContent = $this->generateZoneFile($domain->name);
            
            // Write zone file to temporary location first
            $tempFile = "/tmp/bind-zone-{$domain->name}.tmp";
            file_put_contents($tempFile, $zoneContent);
            
            // Move to zones directory with proper permissions
            $this->shell->exec("sudo mv {$tempFile} {$zoneFile}");
            $this->shell->exec("sudo chown bind:bind {$zoneFile}");
            $this->shell->exec("sudo chmod 644 {$zoneFile}");
            
            // Add zone to named.conf.local
            $this->addZoneToConfig($domain->name);
            
            // Reload BIND9
            $this->reloadBind();
            
            return true;
        } catch (\Exception $e) {
            error_log("BIND9 zone creation failed: " . $e->getMessage());
            return false;
        }
    }

    public function deleteZone(Domain $domain): bool
    {
        try {
            $zoneFile = $this->getZoneFilePath($domain->name);
            
            // Remove zone file
            if (file_exists($zoneFile)) {
                $this->shell->exec("sudo rm -f {$zoneFile}");
            }
            
            // Remove zone from named.conf.local
            $this->removeZoneFromConfig($domain->name);
            
            // Reload BIND9
            $this->reloadBind();
            
            return true;
        } catch (\Exception $e) {
            error_log("BIND9 zone deletion failed: " . $e->getMessage());
            return false;
        }
    }

    public function addRecord(DnsRecord $record): bool
    {
        try {
            // Get domain from our records repository
            $domainRepo = new \App\Repositories\DomainRepository();
            $domain = $domainRepo->find($record->domainId);
            
            if (!$domain) {
                throw new \RuntimeException("Domain not found");
            }
            
            $zoneFile = $this->getZoneFilePath($domain->name);
            
            if (!file_exists($zoneFile)) {
                throw new \RuntimeException("Zone file not found for {$domain->name}");
            }
            
            // Read current zone file
            $zoneContent = file_get_contents($zoneFile);
            
            // Parse and increment serial
            $zoneContent = $this->incrementSerial($zoneContent);
            
            // Add new record
            $recordLine = $this->formatRecord($record, $domain->name);
            $zoneContent .= "\n" . $recordLine;
            
            // Write to temporary file
            $tempFile = "/tmp/bind-zone-{$domain->name}.tmp";
            file_put_contents($tempFile, $zoneContent);
            
            // Validate zone file
            $result = $this->shell->exec("named-checkzone {$domain->name} {$tempFile}");
            if (strpos($result, 'OK') === false) {
                unlink($tempFile);
                throw new \RuntimeException("Zone file validation failed");
            }
            
            // Move to zones directory
            $this->shell->exec("sudo mv {$tempFile} {$zoneFile}");
            $this->shell->exec("sudo chown bind:bind {$zoneFile}");
            $this->shell->exec("sudo chmod 644 {$zoneFile}");
            
            // Reload BIND9
            $this->reloadBind();
            
            return true;
        } catch (\Exception $e) {
            error_log("BIND9 record addition failed: " . $e->getMessage());
            return false;
        }
    }

    public function updateRecord(DnsRecord $record): bool
    {
        // For BIND9, we update by removing old record and adding new one
        return $this->deleteRecord($record) && $this->addRecord($record);
    }

    public function deleteRecord(DnsRecord $record): bool
    {
        try {
            $domainRepo = new \App\Repositories\DomainRepository();
            $domain = $domainRepo->find($record->domainId);
            
            if (!$domain) {
                return false;
            }
            
            $zoneFile = $this->getZoneFilePath($domain->name);
            
            if (!file_exists($zoneFile)) {
                return false;
            }
            
            // Read current zone file
            $zoneContent = file_get_contents($zoneFile);
            $lines = explode("\n", $zoneContent);
            
            // Parse and increment serial
            $zoneContent = $this->incrementSerial($zoneContent);
            $lines = explode("\n", $zoneContent);
            
            // Format the record we're looking for
            $recordLine = $this->formatRecord($record, $domain->name);
            $recordParts = preg_split('/\s+/', trim($recordLine));
            
            // Remove matching record
            $filteredLines = [];
            foreach ($lines as $line) {
                $trimmedLine = trim($line);
                
                // Skip the record if it matches
                if ($this->recordMatches($trimmedLine, $recordParts)) {
                    continue;
                }
                
                $filteredLines[] = $line;
            }
            
            $zoneContent = implode("\n", $filteredLines);
            
            // Write to temporary file
            $tempFile = "/tmp/bind-zone-{$domain->name}.tmp";
            file_put_contents($tempFile, $zoneContent);
            
            // Validate zone file
            $result = $this->shell->exec("named-checkzone {$domain->name} {$tempFile}");
            if (strpos($result, 'OK') === false) {
                unlink($tempFile);
                throw new \RuntimeException("Zone file validation failed");
            }
            
            // Move to zones directory
            $this->shell->exec("sudo mv {$tempFile} {$zoneFile}");
            $this->shell->exec("sudo chown bind:bind {$zoneFile}");
            $this->shell->exec("sudo chmod 644 {$zoneFile}");
            
            // Reload BIND9
            $this->reloadBind();
            
            return true;
        } catch (\Exception $e) {
            error_log("BIND9 record deletion failed: " . $e->getMessage());
            return false;
        }
    }

    private function getZoneFilePath(string $domainName): string
    {
        return $this->zonesPath . '/db.' . $domainName;
    }

    private function generateZoneFile(string $domainName): string
    {
        $serial = date('Ymd') . '01'; // YYYYMMDD01
        $primaryNs = 'ns1.' . $domainName . '.';
        $hostmaster = 'hostmaster.' . $domainName . '.';
        
        return <<<ZONE
\$TTL 3600
@       IN      SOA     {$primaryNs} {$hostmaster} (
                        {$serial}       ; Serial (YYYYMMDDNN)
                        10800           ; Refresh (3 hours)
                        3600            ; Retry (1 hour)
                        604800          ; Expire (7 days)
                        3600            ; Negative Cache TTL (1 hour)
                        )

; Name servers
@       IN      NS      ns1.{$domainName}.
@       IN      NS      ns2.{$domainName}.

ZONE;
    }

    private function formatRecord(DnsRecord $record, string $domainName): string
    {
        $name = $record->name === '@' ? '@' : $record->name;
        $ttl = $record->ttl ?? 3600;
        $type = $record->type;
        $content = $record->content;
        
        // Add trailing dot to FQDN content for certain record types
        if (in_array($type, ['CNAME', 'MX', 'NS']) && substr($content, -1) !== '.') {
            $content .= '.';
        }
        
        // Format with priority for MX records
        if ($type === 'MX' && $record->priority) {
            return sprintf("%-23s IN  %-7s %d %s", $name, $type, $record->priority, $content);
        }
        
        return sprintf("%-23s IN  %-7s %s", $name, $type, $content);
    }

    private function incrementSerial(string $zoneContent): string
    {
        // Match and increment serial in SOA record
        $pattern = '/(\d{10})\s*;\s*Serial/';
        if (preg_match($pattern, $zoneContent, $matches)) {
            $currentSerial = $matches[1];
            $today = date('Ymd');
            $serialDate = substr($currentSerial, 0, 8);
            $serialNum = (int)substr($currentSerial, 8, 2);
            
            if ($serialDate === $today) {
                $newSerial = $today . str_pad($serialNum + 1, 2, '0', STR_PAD_LEFT);
            } else {
                $newSerial = $today . '01';
            }
            
            $zoneContent = preg_replace($pattern, $newSerial . ' ; Serial', $zoneContent);
        }
        
        return $zoneContent;
    }

    private function recordMatches(string $line, array $recordParts): bool
    {
        if (empty($line) || $line[0] === ';') {
            return false;
        }
        
        $lineParts = preg_split('/\s+/', $line);
        
        // Match name and type at minimum
        if (count($lineParts) < 3 || count($recordParts) < 3) {
            return false;
        }
        
        // Compare name and type
        return $lineParts[0] === $recordParts[0] && $lineParts[2] === $recordParts[2];
    }

    private function addZoneToConfig(string $domainName): void
    {
        $zoneFile = $this->getZoneFilePath($domainName);
        
        $zoneConfig = <<<CONFIG

zone "{$domainName}" {
    type master;
    file "{$zoneFile}";
    allow-transfer { any; };
};

CONFIG;
        
        // Append to named.conf.local
        $tempFile = "/tmp/bind-named.conf.tmp";
        
        // Read existing config or create new
        if (file_exists($this->namedConfPath)) {
            $existingConfig = file_get_contents($this->namedConfPath);
            
            // Check if zone already exists
            if (strpos($existingConfig, "zone \"{$domainName}\"") !== false) {
                return; // Zone already configured
            }
            
            file_put_contents($tempFile, $existingConfig . $zoneConfig);
        } else {
            file_put_contents($tempFile, $zoneConfig);
        }
        
        // Validate config
        $result = $this->shell->exec("named-checkconf {$tempFile}");
        if (!empty($result) && strpos($result, 'error') !== false) {
            unlink($tempFile);
            throw new \RuntimeException("BIND configuration validation failed: " . $result);
        }
        
        // Move to config location
        $this->shell->exec("sudo mv {$tempFile} {$this->namedConfPath}");
        $this->shell->exec("sudo chown root:bind {$this->namedConfPath}");
        $this->shell->exec("sudo chmod 644 {$this->namedConfPath}");
    }

    private function removeZoneFromConfig(string $domainName): void
    {
        if (!file_exists($this->namedConfPath)) {
            return;
        }
        
        $config = file_get_contents($this->namedConfPath);
        
        // Remove zone block
        $pattern = '/zone\s+"' . preg_quote($domainName, '/') . '"\s+\{[^}]+\};\s*/';
        $config = preg_replace($pattern, '', $config);
        
        // Write to temporary file
        $tempFile = "/tmp/bind-named.conf.tmp";
        file_put_contents($tempFile, $config);
        
        // Validate config
        $result = $this->shell->exec("named-checkconf {$tempFile}");
        if (!empty($result) && strpos($result, 'error') !== false) {
            unlink($tempFile);
            throw new \RuntimeException("BIND configuration validation failed: " . $result);
        }
        
        // Move to config location
        $this->shell->exec("sudo mv {$tempFile} {$this->namedConfPath}");
        $this->shell->exec("sudo chown root:bind {$this->namedConfPath}");
        $this->shell->exec("sudo chmod 644 {$this->namedConfPath}");
    }

    private function reloadBind(): void
    {
        $this->shell->exec("sudo systemctl reload bind9");
    }
}
