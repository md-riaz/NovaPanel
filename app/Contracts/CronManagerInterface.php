<?php

namespace App\Contracts;

use App\Domain\Entities\Account;
use App\Domain\Entities\CronJob;

interface CronManagerInterface
{
    /**
     * Create a new cron job for an account
     *
     * @param Account $account
     * @param CronJob $job
     * @return bool
     */
    public function createJob(Account $account, CronJob $job): bool;

    /**
     * Update a cron job
     *
     * @param Account $account
     * @param CronJob $job
     * @return bool
     */
    public function updateJob(Account $account, CronJob $job): bool;

    /**
     * Delete a cron job
     *
     * @param Account $account
     * @param CronJob $job
     * @return bool
     */
    public function deleteJob(Account $account, CronJob $job): bool;

    /**
     * List all cron jobs for an account
     *
     * @param Account $account
     * @return array
     */
    public function listJobs(Account $account): array;
}
