<?php

namespace App\Contracts;

use App\Domain\Entities\User;
use App\Domain\Entities\CronJob;

interface CronManagerInterface
{
    /**
     * Create a new cron job for a user
     *
     * @param User $user
     * @param CronJob $job
     * @return bool
     */
    public function createJob(User $user, CronJob $job): bool;

    /**
     * Update a cron job
     *
     * @param User $user
     * @param CronJob $job
     * @return bool
     */
    public function updateJob(User $user, CronJob $job): bool;

    /**
     * Delete a cron job
     *
     * @param User $user
     * @param CronJob $job
     * @return bool
     */
    public function deleteJob(User $user, CronJob $job): bool;

    /**
     * List all cron jobs for a user
     *
     * @param User $user
     * @return array
     */
    public function listJobs(User $user): array;
}
