<?php

namespace App\Contracts;

use App\Domain\Entities\Site;

interface WebServerManagerInterface
{
    /**
     * Create a new site configuration
     *
     * @param Site $site
     * @return bool
     */
    public function createSite(Site $site): bool;

    /**
     * Update an existing site configuration
     *
     * @param Site $site
     * @return bool
     */
    public function updateSite(Site $site): bool;

    /**
     * Delete a site configuration
     *
     * @param Site $site
     * @return bool
     */
    public function deleteSite(Site $site): bool;

    /**
     * Reload the web server to apply configuration changes
     *
     * @return bool
     */
    public function reload(): bool;
}
