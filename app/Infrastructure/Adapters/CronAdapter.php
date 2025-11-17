<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\CronManagerInterface;
use App\Contracts\ShellInterface;
use App\Domain\Entities\User;
use App\Domain\Entities\CronJob;

class CronAdapter implements CronManagerInterface
{
    public function __construct(
        private ShellInterface $shell
    ) {}

    public function createJob(User $user, CronJob $job): bool
    {
        if (!$job->enabled) {
            return true;
        }

        $cronLine = $this->formatCronLine($job);
        
        // Single VPS model: all cron jobs run under the panel user (novapanel)
        // We'll prefix the command with a comment to identify which panel user owns it
        $cronLine = "# NovaPanel user: {$user->username}\n" . $cronLine;
        
        // Get existing crontab for the panel user
        $currentCrontab = $this->getCurrentCrontab();
        
        // Add new job
        $newCrontab = $currentCrontab . "\n" . $cronLine;
        
        // Write back to crontab
        return $this->writeCrontab($newCrontab);
    }

    public function updateJob(User $user, CronJob $job): bool
    {
        // Remove old job and add new one
        $this->deleteJob($user, $job);
        return $this->createJob($user, $job);
    }

    public function deleteJob(User $user, CronJob $job): bool
    {
        $currentCrontab = $this->getCurrentCrontab();
        $lines = explode("\n", $currentCrontab);
        
        // Filter out the job to delete
        $newLines = array_filter($lines, function($line) use ($job) {
            return !str_contains($line, $job->command);
        });
        
        $newCrontab = implode("\n", $newLines);
        
        return $this->writeCrontab($newCrontab);
    }

    public function listJobs(User $user): array
    {
        $crontab = $this->getCurrentCrontab();
        $lines = explode("\n", trim($crontab));
        $jobs = [];
        
        foreach ($lines as $line) {
            $line = trim($line);
            if (empty($line) || str_starts_with($line, '#')) {
                continue;
            }
            
            $jobs[] = $line;
        }
        
        return $jobs;
    }

    private function getCurrentCrontab(): string
    {
        // Single VPS model: read novapanel user's crontab using sudo
        $result = $this->shell->executeSudo('crontab', ['-u', 'novapanel', '-l']);
        
        if ($result['exitCode'] === 0) {
            return $result['output'];
        }
        
        return '';
    }

    private function writeCrontab(string $content): bool
    {
        $tempFile = tempnam(sys_get_temp_dir(), 'crontab');
        file_put_contents($tempFile, $content);
        
        // Single VPS model: write to novapanel user's crontab using sudo
        $result = $this->shell->executeSudo('crontab', ['-u', 'novapanel', $tempFile]);
        
        unlink($tempFile);
        
        return $result['exitCode'] === 0;
    }

    private function formatCronLine(CronJob $job): string
    {
        return "{$job->schedule} {$job->command}";
    }
}
