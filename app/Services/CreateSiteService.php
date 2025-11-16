<?php

namespace App\Services;

use App\Domain\Entities\Site;
use App\Domain\Entities\PhpRuntime;
use App\Repositories\SiteRepository;
use App\Repositories\AccountRepository;
use App\Contracts\WebServerManagerInterface;
use App\Contracts\PhpRuntimeManagerInterface;

class CreateSiteService
{
    public function __construct(
        private SiteRepository $siteRepository,
        private AccountRepository $accountRepository,
        private WebServerManagerInterface $webServerManager,
        private PhpRuntimeManagerInterface $phpRuntimeManager
    ) {}

    public function execute(
        int $accountId,
        string $domain,
        string $phpVersion = '8.2',
        bool $sslEnabled = false
    ): Site {
        // Validate domain
        if (!$this->isValidDomain($domain)) {
            throw new \InvalidArgumentException('Invalid domain format');
        }

        // Check if site already exists
        if ($this->siteRepository->findByDomain($domain)) {
            throw new \RuntimeException("Site with domain '$domain' already exists");
        }

        // Get account
        $account = $this->accountRepository->find($accountId);
        if (!$account) {
            throw new \RuntimeException("Account not found");
        }

        // Set document root
        $documentRoot = "{$account->homeDirectory}/public_html/{$domain}";

        // Create site entity
        $site = new Site(
            accountId: $accountId,
            domain: $domain,
            documentRoot: $documentRoot,
            phpVersion: $phpVersion,
            sslEnabled: $sslEnabled
        );

        // Save to database first
        $site = $this->siteRepository->create($site);

        try {
            // Create document root directory
            mkdir($documentRoot, 0755, true);
            chown($documentRoot, $account->username);
            chgrp($documentRoot, $account->username);

            // Create PHP-FPM pool
            $runtime = new PhpRuntime(
                version: $phpVersion,
                binary: "/usr/bin/php{$phpVersion}",
                fpmSocket: "/var/run/php/php{$phpVersion}-fpm.sock"
            );
            $this->phpRuntimeManager->createPool($site, $runtime);

            // Create Nginx vhost
            $this->webServerManager->createSite($site);

            // Create default index.php
            $indexContent = "<?php\nphpinfo();\n";
            file_put_contents("{$documentRoot}/index.php", $indexContent);
            chown("{$documentRoot}/index.php", $account->username);

        } catch (\Exception $e) {
            // Rollback: delete from database if infrastructure setup fails
            $this->siteRepository->delete($site->id);
            throw new \RuntimeException("Failed to create site infrastructure: " . $e->getMessage());
        }

        return $site;
    }

    private function isValidDomain(string $domain): bool
    {
        return (bool) preg_match('/^[a-z0-9]+([\-\.]{1}[a-z0-9]+)*\.[a-z]{2,}$/', $domain);
    }
}
