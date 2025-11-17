<?php

namespace App\Services;

use App\Domain\Entities\Site;
use App\Domain\Entities\PhpRuntime;
use App\Repositories\SiteRepository;
use App\Repositories\UserRepository;
use App\Contracts\WebServerManagerInterface;
use App\Contracts\PhpRuntimeManagerInterface;
use App\Contracts\ShellInterface;

class CreateSiteService
{
    public function __construct(
        private SiteRepository $siteRepository,
        private UserRepository $userRepository,
        private WebServerManagerInterface $webServerManager,
        private PhpRuntimeManagerInterface $phpRuntimeManager,
        private ShellInterface $shell
    ) {}

    public function execute(
        int $userId,
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

        // Get user
        $user = $this->userRepository->find($userId);
        if (!$user) {
            throw new \RuntimeException("User not found");
        }

        // Set document root under panel's directory structure
        // All sites run under the panel user, organized by owner username
        $baseDir = "/opt/novapanel/sites/{$user->username}";
        $documentRoot = "{$baseDir}/{$domain}";

        // Create site entity
        $site = new Site(
            userId: $userId,
            domain: $domain,
            documentRoot: $documentRoot,
            phpVersion: $phpVersion,
            sslEnabled: $sslEnabled,
            ownerUsername: $user->username
        );

        // Save to database first
        $site = $this->siteRepository->create($site);

        try {
            // Create base directory for user's sites if it doesn't exist
            if (!is_dir($baseDir)) {
                $result = $this->shell->executeSudo('mkdir', ['-p', $baseDir]);
                if ($result['exitCode'] !== 0) {
                    throw new \RuntimeException("Failed to create base directory: " . $result['output']);
                }
                // Set ownership to novapanel:www-data with group write permissions
                $this->shell->executeSudo('chown', ['novapanel:www-data', $baseDir]);
                $this->shell->executeSudo('chmod', ['775', $baseDir]);
            }
            
            // Create document root directory
            $result = $this->shell->executeSudo('mkdir', ['-p', $documentRoot]);
            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException("Failed to create document root: " . $result['output']);
            }
            // Set ownership to novapanel:www-data with group write permissions
            $this->shell->executeSudo('chown', ['novapanel:www-data', $documentRoot]);
            $this->shell->executeSudo('chmod', ['775', $documentRoot]);

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
            $indexContent = "<?php\necho '<h1>Welcome to {$domain}</h1>';\necho '<p>Site owner: {$user->username}</p>';\nphpinfo();\n";
            file_put_contents("{$documentRoot}/index.php", $indexContent);

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
