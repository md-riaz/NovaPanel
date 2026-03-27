<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\ShellInterface;
use App\Contracts\WebServerManagerInterface;
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

        $writeResult = $this->shell->writeFile($vhostFile, $vhostContent);
        if ($writeResult['exitCode'] !== 0) {
            throw new \RuntimeException('Failed to write Nginx configuration: ' . $writeResult['output']);
        }

        $this->shell->executeSudo('ln', ['-sf', $vhostFile, self::VHOST_ENABLED . '/' . $site->domain . '.conf']);

        $result = $this->shell->executeSudo('nginx', ['-t']);
        if ($result['exitCode'] !== 0) {
            $this->deleteSite($site);
            throw new \RuntimeException('Invalid Nginx configuration: ' . $result['output']);
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

        if (file_exists($enabledLink)) {
            $this->shell->executeSudo('rm', ['-f', $enabledLink]);
        }

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
        $blocks = [$this->generateHttpServerBlock($site)];

        if ($site->hasActiveCertificate()) {
            $blocks[] = $this->generateHttpsServerBlock($site);
        }

        return implode("\n\n", $blocks) . "\n";
    }

    private function generateHttpServerBlock(Site $site): string
    {
        $lines = [
            'server {',
            '    listen 80;',
            '    listen [::]:80;',
            "    server_name {$site->domain};",
            "    root {$site->documentRoot};",
            '    index index.php index.html index.htm;',
            '',
            '    location ^~ /.well-known/acme-challenge/ {',
            "        root {$site->documentRoot};",
            '        default_type text/plain;',
            '        allow all;',
            '    }',
            '',
        ];

        if ($site->hasActiveCertificate() && $site->forceHttps) {
            $lines[] = '    if ($request_uri !~ "^/\\.well-known/acme-challenge/") {';
            $lines[] = '        return 301 https://$host$request_uri;';
            $lines[] = '    }';
            $lines[] = '';
        }

        $lines = [
            ...$lines,
            ...$this->applicationLocationBlock(),
            '',
            ...$this->phpLocationBlock($site),
            '',
            '    location ~ /\.ht {',
            '        deny all;',
            '    }',
            '}',
        ];

        return implode("\n", $lines);
    }

    private function generateHttpsServerBlock(Site $site): string
    {
        $lines = [
            'server {',
            '    listen 443 ssl;',
            '    listen [::]:443 ssl;',
            '    http2 on;',
            "    server_name {$site->domain};",
            "    root {$site->documentRoot};",
            '    index index.php index.html index.htm;',
            '',
            "    ssl_certificate {$site->certificatePath};",
            "    ssl_certificate_key {$site->certificateKeyPath};",
            '    ssl_session_timeout 1d;',
            '    ssl_session_cache shared:NovaPanelSSL:10m;',
            '    ssl_protocols TLSv1.2 TLSv1.3;',
            '    ssl_prefer_server_ciphers off;',
            '',
        ];

        if ($site->forceHttps) {
            $lines[] = '    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains; preload" always;';
            $lines[] = '';
        }

        $lines = [
            ...$lines,
            ...$this->applicationLocationBlock(),
            '',
            ...$this->phpLocationBlock($site),
            '',
            '    location ~ /\.ht {',
            '        deny all;',
            '    }',
            '}',
        ];

        return implode("\n", $lines);
    }

    private function applicationLocationBlock(): array
    {
        return [
            '    location / {',
            '        try_files $uri $uri/ /index.php?$query_string;',
            '    }',
        ];
    }

    private function phpLocationBlock(Site $site): array
    {
        $phpSocket = $this->getPhpFpmSocket($site->phpVersion, $site->domain);

        return [
            '    location ~ \.php$ {',
            "        fastcgi_pass unix:{$phpSocket};",
            '        fastcgi_index index.php;',
            '        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;',
            '        include fastcgi_params;',
            '    }',
        ];
    }

    private function getPhpFpmSocket(string $version, string $domain): string
    {
        return "/var/run/php/php{$version}-fpm-{$domain}.sock";
    }
}
