<?php

namespace App\Services;

use App\Contracts\PhpRuntimeManagerInterface;
use App\Contracts\ShellInterface;
use App\Contracts\WebServerManagerInterface;
use App\Domain\Entities\PhpRuntime;
use App\Domain\Entities\Site;
use App\Repositories\SiteRepository;
use App\Repositories\UserRepository;

class CreateSiteService
{
    public function __construct(
        private SiteRepository $siteRepository,
        private UserRepository $userRepository,
        private WebServerManagerInterface $webServerManager,
        private PhpRuntimeManagerInterface $phpRuntimeManager,
        private ShellInterface $shell,
        private AcmeCertificateService $acmeCertificateService
    ) {}

    public function execute(
        int $userId,
        string $domain,
        string $phpVersion = '8.2',
        bool $requestCertificate = false,
        string $validationMethod = 'webroot',
        bool $autoRenew = true,
        bool $forceHttps = false,
        string $provider = 'letsencrypt'
    ): Site {
        if (!$this->isValidDomain($domain)) {
            throw new \InvalidArgumentException('Invalid domain format');
        }

        if ($this->siteRepository->findByDomain($domain)) {
            throw new \RuntimeException("Site with domain '{$domain}' already exists");
        }

        $user = $this->userRepository->find($userId);
        if (!$user) {
            throw new \RuntimeException('User not found');
        }

        $availableVersions = $this->phpRuntimeManager->listAvailable();
        $versionExists = false;
        foreach ($availableVersions as $runtime) {
            if ($runtime->version === $phpVersion) {
                $versionExists = true;
                break;
            }
        }

        if (!$versionExists) {
            throw new \InvalidArgumentException("PHP version {$phpVersion} is not installed on this system. Please install it first or choose an available version.");
        }

        $baseDir = "/opt/novapanel/sites/{$user->username}";
        $documentRoot = "{$baseDir}/{$domain}";

        $site = new Site(
            userId: $userId,
            domain: $domain,
            documentRoot: $documentRoot,
            phpVersion: $phpVersion,
            sslEnabled: false,
            certificateProvider: $provider,
            certificateStatus: $requestCertificate ? 'pending' : 'unissued',
            certificateAutoRenew: $autoRenew,
            certificateValidationMethod: $validationMethod,
            forceHttps: $forceHttps,
            ownerUsername: $user->username
        );

        $site = $this->siteRepository->create($site);

        try {
            if (!is_dir($baseDir)) {
                $result = $this->shell->executeSudo('mkdir', ['-p', $baseDir]);
                if ($result['exitCode'] !== 0) {
                    throw new \RuntimeException('Failed to create base directory: ' . $result['output']);
                }
                $this->shell->executeSudo('chown', ['novapanel:www-data', $baseDir]);
                $this->shell->executeSudo('chmod', ['775', $baseDir]);
            }

            $result = $this->shell->executeSudo('mkdir', ['-p', $documentRoot]);
            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException('Failed to create document root: ' . $result['output']);
            }
            $this->shell->executeSudo('chown', ['novapanel:www-data', $documentRoot]);
            $this->shell->executeSudo('chmod', ['775', $documentRoot]);

            $runtime = new PhpRuntime(
                version: $phpVersion,
                binary: "/usr/bin/php{$phpVersion}",
                fpmSocket: "/var/run/php/php{$phpVersion}-fpm.sock"
            );
            $this->phpRuntimeManager->createPool($site, $runtime);
            $this->webServerManager->createSite($site);

            $indexContent = "<?php\necho '<h1>Welcome to {$domain}</h1>';\necho '<p>Site owner: {$user->username}</p>';\nphpinfo();\n";
            $writeResult = $this->shell->writeFile("{$documentRoot}/index.php", $indexContent);
            if ($writeResult['exitCode'] !== 0) {
                throw new \RuntimeException('Failed to write default index file: ' . $writeResult['output']);
            }
        } catch (\Throwable $exception) {
            error_log("Site creation failed for domain {$domain}: " . $exception->getMessage());

            try {
                $this->phpRuntimeManager->deletePool($site);
            } catch (\Throwable $poolError) {
                error_log("Failed to rollback PHP-FPM pool for {$domain}: " . $poolError->getMessage());
            }

            try {
                $this->webServerManager->deleteSite($site);
            } catch (\Throwable $vhostError) {
                error_log("Failed to rollback Nginx vhost for {$domain}: " . $vhostError->getMessage());
            }

            try {
                if (is_dir($documentRoot)) {
                    $this->shell->executeSudo('rm', ['-rf', $documentRoot]);
                }
            } catch (\Throwable $dirError) {
                error_log("Failed to rollback directory {$documentRoot}: " . $dirError->getMessage());
            }

            $this->siteRepository->delete($site->id);

            throw new \RuntimeException('Failed to create site infrastructure: ' . $exception->getMessage());
        }

        if ($requestCertificate) {
            try {
                $site = $this->acmeCertificateService->issue(
                    $site,
                    $provider,
                    $validationMethod,
                    $autoRenew,
                    $forceHttps
                );
            } catch (\Throwable $exception) {
                error_log("Certificate issuance failed for {$domain}: " . $exception->getMessage());
                $site->certificateStatus = 'failed';
                $site->lastCertificateError = $exception->getMessage();
                $this->siteRepository->update($site);
            }
        }

        return $site;
    }

    private function isValidDomain(string $domain): bool
    {
        return (bool) preg_match('/^[a-z0-9]+([\-\.]{1}[a-z0-9]+)*\.[a-z]{2,}$/', $domain);
    }
}
