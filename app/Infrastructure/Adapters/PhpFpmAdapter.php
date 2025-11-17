<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\PhpRuntimeManagerInterface;
use App\Contracts\ShellInterface;
use App\Domain\Entities\Site;
use App\Domain\Entities\PhpRuntime;

class PhpFpmAdapter implements PhpRuntimeManagerInterface
{
    private const POOL_PATH = '/etc/php/{version}/fpm/pool.d';

    public function __construct(
        private ShellInterface $shell
    ) {}

    public function listAvailable(): array
    {
        $versions = ['7.4', '8.0', '8.1', '8.2', '8.3'];
        $available = [];
        
        foreach ($versions as $version) {
            $binary = "/usr/bin/php{$version}";
            if (file_exists($binary)) {
                $available[] = new PhpRuntime(
                    version: $version,
                    binary: $binary,
                    fpmSocket: "/var/run/php/php{$version}-fpm.sock"
                );
            }
        }
        
        return $available;
    }

    public function assignRuntimeToSite(Site $site, PhpRuntime $runtime): bool
    {
        $site->phpVersion = $runtime->version;
        return $this->createPool($site, $runtime);
    }

    public function createPool(Site $site, PhpRuntime $runtime): bool
    {
        $poolContent = $this->generatePoolConfig($site, $runtime);
        $poolPath = str_replace('{version}', $runtime->version, self::POOL_PATH);
        $poolFile = "{$poolPath}/{$site->domain}.conf";
        
        // Write pool configuration using sudo
        $writeResult = $this->shell->writeFile($poolFile, $poolContent);
        
        if ($writeResult['exitCode'] !== 0) {
            throw new \RuntimeException("Failed to write PHP-FPM pool configuration: " . $writeResult['output']);
        }
        
        // Reload PHP-FPM
        $result = $this->shell->executeSudo('systemctl', ['reload', "php{$runtime->version}-fpm"]);
        
        return $result['exitCode'] === 0;
    }

    public function deletePool(Site $site): bool
    {
        $poolPath = str_replace('{version}', $site->phpVersion, self::POOL_PATH);
        $poolFile = "{$poolPath}/{$site->domain}.conf";
        
        if (file_exists($poolFile)) {
            $this->shell->executeSudo('rm', ['-f', $poolFile]);
            $this->shell->executeSudo('systemctl', ['reload', "php{$site->phpVersion}-fpm"]);
        }
        
        return true;
    }

    private function generatePoolConfig(Site $site, PhpRuntime $runtime): string
    {
        $poolName = str_replace('.', '_', $site->domain);
        
        // Single VPS model: all sites run under the panel user (novapanel)
        $username = 'novapanel';
        
        $config = "[{$poolName}]\n";
        $config .= "user = {$username}\n";
        $config .= "group = {$username}\n";
        $config .= "listen = /var/run/php/php{$runtime->version}-fpm-{$site->domain}.sock\n";
        $config .= "listen.owner = www-data\n";
        $config .= "listen.group = www-data\n";
        $config .= "listen.mode = 0660\n\n";
        
        $config .= "pm = dynamic\n";
        $config .= "pm.max_children = 5\n";
        $config .= "pm.start_servers = 2\n";
        $config .= "pm.min_spare_servers = 1\n";
        $config .= "pm.max_spare_servers = 3\n\n";
        
        $config .= "chdir = {$site->documentRoot}\n";
        $config .= "php_admin_value[upload_tmp_dir] = {$site->documentRoot}/tmp\n";
        $config .= "php_admin_value[session.save_path] = {$site->documentRoot}/tmp\n";
        
        return $config;
    }
}
