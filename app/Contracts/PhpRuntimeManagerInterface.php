<?php

namespace App\Contracts;

use App\Domain\Entities\Site;
use App\Domain\Entities\PhpRuntime;

interface PhpRuntimeManagerInterface
{
    /**
     * List all available PHP runtimes
     *
     * @return array
     */
    public function listAvailable(): array;

    /**
     * Assign a PHP runtime to a site
     *
     * @param Site $site
     * @param PhpRuntime $runtime
     * @return bool
     */
    public function assignRuntimeToSite(Site $site, PhpRuntime $runtime): bool;

    /**
     * Create a PHP-FPM pool for a site
     *
     * @param Site $site
     * @param PhpRuntime $runtime
     * @return bool
     */
    public function createPool(Site $site, PhpRuntime $runtime): bool;

    /**
     * Delete a PHP-FPM pool
     *
     * @param Site $site
     * @return bool
     */
    public function deletePool(Site $site): bool;
}
