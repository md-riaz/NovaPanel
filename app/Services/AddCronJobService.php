<?php

namespace App\Services;

use App\Domain\Entities\CronJob;
use App\Repositories\CronJobRepository;
use App\Repositories\UserRepository;
use App\Contracts\CronManagerInterface;

class AddCronJobService
{
    public function __construct(
        private CronJobRepository $cronJobRepository,
        private UserRepository $userRepository,
        private CronManagerInterface $cronManager
    ) {}

    public function execute(
        int $userId,
        string $schedule,
        string $command,
        bool $enabled = true
    ): CronJob {
        // Validate cron schedule
        if (!$this->isValidCronSchedule($schedule)) {
            throw new \InvalidArgumentException('Invalid cron schedule format');
        }

        // Validate command
        if (empty(trim($command))) {
            throw new \InvalidArgumentException('Command cannot be empty');
        }

        // Get panel user
        $user = $this->userRepository->find($userId);
        if (!$user) {
            throw new \RuntimeException("User not found");
        }

        // Create cron job entity
        $cronJob = new CronJob(
            userId: $userId,
            schedule: $schedule,
            command: $command,
            enabled: $enabled
        );

        // Save to panel database
        $cronJob = $this->cronJobRepository->create($cronJob);

        try {
            // Add to system crontab
            if ($enabled && !$this->cronManager->createJob($user, $cronJob)) {
                throw new \RuntimeException("Failed to add cron job to system crontab");
            }

        } catch (\Exception $e) {
            // Rollback: delete from panel database if infrastructure setup fails
            $this->cronJobRepository->delete($cronJob->id);
            throw new \RuntimeException("Failed to create cron job: " . $e->getMessage());
        }

        return $cronJob;
    }

    private function isValidCronSchedule(string $schedule): bool
    {
        // Basic validation: should have 5 parts (minute hour day month weekday)
        $parts = preg_split('/\s+/', trim($schedule));
        
        if (count($parts) !== 5) {
            return false;
        }

        // Each part should be valid cron syntax (* or number or range or */n)
        foreach ($parts as $part) {
            if (!preg_match('/^(\*|[0-9,-\/]+)$/', $part)) {
                return false;
            }
        }

        return true;
    }
}
