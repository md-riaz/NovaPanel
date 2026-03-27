<?php

namespace App\Services;

use App\Contracts\WebServerManagerInterface;
use App\Domain\Entities\Site;
use App\Repositories\SiteRepository;
use App\Support\AuditLogger;

class SiteHttpsService
{
    public function __construct(
        private SiteRepository $siteRepository,
        private WebServerManagerInterface $webServerManager
    ) {}

    public function updateForceHttps(Site $site, bool $forceHttps): Site
    {
        $site->forceHttps = $forceHttps;

        // Apply the web server configuration first to avoid persisting stale DB state on failures.
        $this->webServerManager->updateSite($site);
        $this->siteRepository->update($site);

        AuditLogger::logUpdated('site', $site->domain, [
            'site_id' => $site->id,
            'force_https' => $site->forceHttps,
        ]);

        return $site;
    }
}
