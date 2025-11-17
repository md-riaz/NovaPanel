#!/usr/bin/env php
<?php
/**
 * Migration Script: PowerDNS to BIND9
 * 
 * This script migrates DNS zones and records from PowerDNS MySQL database
 * to BIND9 zone files.
 * 
 * Usage: php scripts/migrate-powerdns-to-bind9.php
 * 
 * Prerequisites:
 * - PowerDNS database must be accessible
 * - BIND9 must be installed and configured
 * - Script must be run with appropriate permissions (as novapanel user or root)
 */

require_once __DIR__ . '/../vendor/autoload.php';

// Load environment configuration
if (file_exists(__DIR__ . '/../.env.php')) {
    require_once __DIR__ . '/../.env.php';
}

echo "========================================\n";
echo "PowerDNS to BIND9 Migration Script\n";
echo "========================================\n\n";

// Get PowerDNS credentials
$pdnsHost = getenv('POWERDNS_HOST') ?: 'localhost';
$pdnsDb = getenv('POWERDNS_DATABASE') ?: 'powerdns';
$pdnsUser = getenv('POWERDNS_USER') ?: 'powerdns';
$pdnsPass = getenv('POWERDNS_PASSWORD') ?: '';

if (empty($pdnsUser) || empty($pdnsDb)) {
    echo "❌ PowerDNS credentials not found in configuration.\n";
    echo "Please ensure .env.php has POWERDNS_* environment variables set.\n";
    exit(1);
}

// Connect to PowerDNS database
try {
    $pdo = new PDO(
        "mysql:host={$pdnsHost};dbname={$pdnsDb}",
        $pdnsUser,
        $pdnsPass
    );
    $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
    echo "✓ Connected to PowerDNS database\n\n";
} catch (PDOException $e) {
    echo "❌ Failed to connect to PowerDNS database: " . $e->getMessage() . "\n";
    exit(1);
}

// Get BIND9 configuration
$zonesPath = getenv('BIND9_ZONES_PATH') ?: '/etc/bind/zones';
$namedConfPath = getenv('BIND9_NAMED_CONF_PATH') ?: '/etc/bind/named.conf.local';

// Create zones directory if it doesn't exist
if (!is_dir($zonesPath)) {
    echo "Creating zones directory: {$zonesPath}\n";
    exec("sudo mkdir -p {$zonesPath}");
    exec("sudo chown bind:bind {$zonesPath}");
    exec("sudo chmod 755 {$zonesPath}");
}

// Fetch all domains from PowerDNS
$stmt = $pdo->query("SELECT id, name, type FROM domains ORDER BY name");
$domains = $stmt->fetchAll(PDO::FETCH_ASSOC);

if (empty($domains)) {
    echo "⚠ No domains found in PowerDNS database.\n";
    exit(0);
}

echo "Found " . count($domains) . " domain(s) to migrate:\n\n";

$migrated = 0;
$failed = 0;

foreach ($domains as $domain) {
    $domainName = $domain['name'];
    $domainId = $domain['id'];
    
    echo "Migrating: {$domainName}...\n";
    
    // Fetch all records for this domain
    $stmt = $pdo->prepare("SELECT name, type, content, ttl, prio FROM records WHERE domain_id = ? ORDER BY type, name");
    $stmt->execute([$domainId]);
    $records = $stmt->fetchAll(PDO::FETCH_ASSOC);
    
    // Generate zone file
    $zoneContent = generateZoneFile($domainName, $records);
    
    // Write zone file
    $zoneFile = $zonesPath . '/db.' . $domainName;
    $tempFile = "/tmp/bind-zone-{$domainName}.tmp";
    
    file_put_contents($tempFile, $zoneContent);
    
    // Validate zone file
    exec("named-checkzone {$domainName} {$tempFile} 2>&1", $output, $returnCode);
    
    if ($returnCode !== 0) {
        echo "  ❌ Zone file validation failed:\n";
        echo "     " . implode("\n     ", $output) . "\n";
        $failed++;
        @unlink($tempFile);
        continue;
    }
    
    // Move to zones directory
    exec("sudo mv {$tempFile} {$zoneFile}");
    exec("sudo chown bind:bind {$zoneFile}");
    exec("sudo chmod 644 {$zoneFile}");
    
    // Add zone to named.conf.local
    addZoneToConfig($domainName, $zoneFile, $namedConfPath);
    
    echo "  ✓ Migrated successfully\n";
    $migrated++;
}

echo "\n========================================\n";
echo "Migration Summary\n";
echo "========================================\n";
echo "Total domains: " . count($domains) . "\n";
echo "Migrated: {$migrated}\n";
echo "Failed: {$failed}\n\n";

if ($migrated > 0) {
    echo "Validating BIND configuration...\n";
    exec("sudo named-checkconf", $output, $returnCode);
    
    if ($returnCode === 0) {
        echo "✓ BIND configuration is valid\n\n";
        echo "Reloading BIND9...\n";
        exec("sudo systemctl reload bind9", $output, $returnCode);
        
        if ($returnCode === 0) {
            echo "✓ BIND9 reloaded successfully\n\n";
            echo "========================================\n";
            echo "✅ Migration completed successfully!\n";
            echo "========================================\n\n";
            echo "Next steps:\n";
            echo "1. Verify DNS resolution is working\n";
            echo "2. Update DNS panel configuration to use BIND9\n";
            echo "3. Once verified, you can remove PowerDNS:\n";
            echo "   sudo systemctl stop pdns\n";
            echo "   sudo systemctl disable pdns\n";
            echo "   sudo apt remove pdns-server pdns-backend-mysql\n";
        } else {
            echo "❌ Failed to reload BIND9\n";
            echo "Please check BIND9 logs: sudo journalctl -u bind9 -n 50\n";
        }
    } else {
        echo "❌ BIND configuration validation failed\n";
        echo "Please check the configuration manually\n";
    }
}

function generateZoneFile(string $domainName, array $records): string
{
    $serial = date('Ymd') . '01';
    $ttl = 3600;
    
    // Find SOA record
    $soaRecord = null;
    foreach ($records as $record) {
        if ($record['type'] === 'SOA') {
            $soaRecord = $record;
            break;
        }
    }
    
    // Parse SOA or use defaults
    if ($soaRecord) {
        $soaParts = explode(' ', $soaRecord['content']);
        $primaryNs = $soaParts[0] ?? "ns1.{$domainName}.";
        $hostmaster = $soaParts[1] ?? "hostmaster.{$domainName}.";
        $refresh = $soaParts[3] ?? 10800;
        $retry = $soaParts[4] ?? 3600;
        $expire = $soaParts[5] ?? 604800;
        $negativeTtl = $soaParts[6] ?? 3600;
    } else {
        $primaryNs = "ns1.{$domainName}.";
        $hostmaster = "hostmaster.{$domainName}.";
        $refresh = 10800;
        $retry = 3600;
        $expire = 604800;
        $negativeTtl = 3600;
    }
    
    // Ensure trailing dots
    if (substr($primaryNs, -1) !== '.') {
        $primaryNs .= '.';
    }
    if (substr($hostmaster, -1) !== '.') {
        $hostmaster .= '.';
    }
    
    $zone = "\$TTL {$ttl}\n";
    $zone .= "@       IN      SOA     {$primaryNs} {$hostmaster} (\n";
    $zone .= "                        {$serial}       ; Serial (YYYYMMDDNN)\n";
    $zone .= "                        {$refresh}           ; Refresh\n";
    $zone .= "                        {$retry}            ; Retry\n";
    $zone .= "                        {$expire}          ; Expire\n";
    $zone .= "                        {$negativeTtl}            ; Negative Cache TTL\n";
    $zone .= "                        )\n\n";
    
    // Group records by type
    $recordsByType = [];
    foreach ($records as $record) {
        if ($record['type'] === 'SOA') {
            continue; // Already processed
        }
        
        $type = $record['type'];
        if (!isset($recordsByType[$type])) {
            $recordsByType[$type] = [];
        }
        $recordsByType[$type][] = $record;
    }
    
    // Output records by type
    $typeOrder = ['NS', 'A', 'AAAA', 'CNAME', 'MX', 'TXT', 'SRV', 'PTR'];
    
    foreach ($typeOrder as $type) {
        if (!isset($recordsByType[$type])) {
            continue;
        }
        
        $zone .= "; {$type} records\n";
        foreach ($recordsByType[$type] as $record) {
            $zone .= formatRecord($record, $domainName) . "\n";
        }
        $zone .= "\n";
    }
    
    // Output any remaining record types
    foreach ($recordsByType as $type => $records) {
        if (in_array($type, $typeOrder)) {
            continue;
        }
        
        $zone .= "; {$type} records\n";
        foreach ($records as $record) {
            $zone .= formatRecord($record, $domainName) . "\n";
        }
        $zone .= "\n";
    }
    
    return $zone;
}

function formatRecord(array $record, string $domainName): string
{
    $name = $record['name'];
    
    // Convert FQDN to relative name
    if ($name === $domainName || $name === $domainName . '.') {
        $name = '@';
    } elseif (substr($name, -(strlen($domainName) + 1)) === ".{$domainName}") {
        $name = substr($name, 0, -(strlen($domainName) + 1));
    }
    
    $ttl = $record['ttl'] ?? 3600;
    $type = $record['type'];
    $content = $record['content'];
    
    // Ensure trailing dot for FQDN content
    if (in_array($type, ['CNAME', 'MX', 'NS']) && substr($content, -1) !== '.') {
        $content .= '.';
    }
    
    // Format with priority for MX records
    if ($type === 'MX' && !empty($record['prio'])) {
        return sprintf("%-23s IN  %-7s %d %s", $name, $type, $record['prio'], $content);
    }
    
    return sprintf("%-23s IN  %-7s %s", $name, $type, $content);
}

function addZoneToConfig(string $domainName, string $zoneFile, string $namedConfPath): void
{
    $zoneConfig = <<<CONFIG

zone "{$domainName}" {
    type master;
    file "{$zoneFile}";
    allow-transfer { any; };
};

CONFIG;
    
    // Read existing config
    $existingConfig = '';
    if (file_exists($namedConfPath)) {
        $existingConfig = file_get_contents($namedConfPath);
        
        // Check if zone already exists
        if (strpos($existingConfig, "zone \"{$domainName}\"") !== false) {
            return; // Zone already configured
        }
    }
    
    // Append new zone
    $tempFile = "/tmp/bind-named.conf.tmp";
    file_put_contents($tempFile, $existingConfig . $zoneConfig);
    
    // Validate config
    exec("sudo named-checkconf {$tempFile}", $output, $returnCode);
    if ($returnCode !== 0) {
        @unlink($tempFile);
        throw new RuntimeException("BIND configuration validation failed");
    }
    
    // Move to config location
    exec("sudo mv {$tempFile} {$namedConfPath}");
    exec("sudo chown root:bind {$namedConfPath}");
    exec("sudo chmod 644 {$namedConfPath}");
}
