<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\WebServerManagerInterface;
use App\Contracts\ShellInterface;
use App\Domain\Entities\Site;

class NginxAdapter implements WebServerManagerInterface
{
    private const VHOST_PATH = '/etc/nginx/sites-available';
    private const VHOST_ENABLED = '/etc/nginx/sites-enabled';

    public function __construct(
        private ShellInterface $shell
    ) {}

    public function createSite(Site $site): bool
    {
        $vhostContent = $this->generateVhostConfig($site);
        $vhostFile = self::VHOST_PATH . '/' . $site->domain . '.conf';
        
        // Write vhost configuration using sudo
        $writeResult = $this->shell->writeFile($vhostFile, $vhostContent);
        
        if ($writeResult['exitCode'] !== 0) {
            throw new \RuntimeException("Failed to write Nginx configuration: " . $writeResult['output']);
        }
        
        // Enable site by creating symlink
        $this->shell->executeSudo('ln', ['-sf', $vhostFile, self::VHOST_ENABLED . '/' . $site->domain . '.conf']);
        
        // Test nginx configuration
        $result = $this->shell->executeSudo('nginx', ['-t']);
        
        if ($result['exitCode'] !== 0) {
            // Rollback if config is invalid
            $this->deleteSite($site);
            throw new \RuntimeException("Invalid Nginx configuration: " . $result['output']);
        }
        
        return $this->reload();
    }

    public function updateSite(Site $site): bool
    {
        return $this->createSite($site);
    }

    public function deleteSite(Site $site): bool
    {
        $vhostFile = self::VHOST_PATH . '/' . $site->domain . '.conf';
        $enabledLink = self::VHOST_ENABLED . '/' . $site->domain . '.conf';
        
        // Remove symlink
        if (file_exists($enabledLink)) {
            $this->shell->executeSudo('rm', ['-f', $enabledLink]);
        }
        
        // Remove vhost file
        if (file_exists($vhostFile)) {
            $this->shell->executeSudo('rm', ['-f', $vhostFile]);
        }
        
        return $this->reload();
    }

    public function reload(): bool
    {
        $result = $this->shell->executeSudo('systemctl', ['reload', 'nginx']);
        return $result['exitCode'] === 0;
    }

    private function generateVhostConfig(Site $site): string
    {
        // Use site-specific PHP-FPM socket that matches PhpFpmAdapter
        $phpSocket = $this->getPhpFpmSocket($site->phpVersion, $site->domain);
        
        $config = "server {\n";
        $config .= "    listen 80;\n";
        $config .= "    server_name {$site->domain};\n";
        $config .= "    root {$site->documentRoot};\n";
        $config .= "    index index.php index.html index.htm;\n\n";
        
        $config .= "    location / {\n";
        $config .= "        try_files \$uri \$uri/ /index.php?\$query_string;\n";
        $config .= "    }\n\n";
        
        $config .= "    location ~ \\.php$ {\n";
        $config .= "        fastcgi_pass unix:{$phpSocket};\n";
        $config .= "        fastcgi_index index.php;\n";
        $config .= "        fastcgi_param SCRIPT_FILENAME \$document_root\$fastcgi_script_name;\n";
        $config .= "        include fastcgi_params;\n";
        $config .= "    }\n\n";
        
        $config .= "    location ~ /\\.ht {\n";
        $config .= "        deny all;\n";
        $config .= "    }\n";
        
        if ($site->sslEnabled) {
            $config .= "\n    listen 443 ssl;\n";
            $config .= "    ssl_certificate /etc/ssl/certs/{$site->domain}.crt;\n";
            $config .= "    ssl_certificate_key /etc/ssl/private/{$site->domain}.key;\n";
        }
        
        $config .= "}\n";
        
        return $config;
    }

    private function getPhpFpmSocket(string $version, string $domain): string
    {
        // Return site-specific socket path that matches PhpFpmAdapter configuration
        return "/var/run/php/php{$version}-fpm-{$domain}.sock";
    }
}
