<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\CronManagerInterface;
use App\Contracts\ShellInterface;
use App\Domain\Entities\Account;
use App\Domain\Entities\CronJob;

class CronAdapter implements CronManagerInterface
{
    public function __construct(
        private ShellInterface $shell
    ) {}

    public function createJob(Account $account, CronJob $job): bool
    {
        if (!$job->enabled) {
            return true;
        }

        $cronLine = $this->formatCronLine($job);
        
        // Get existing crontab
        $currentCrontab = $this->getCurrentCrontab($account);
        
        // Add new job
        $newCrontab = $currentCrontab . "\n" . $cronLine;
        
        // Write back to crontab
        return $this->writeCrontab($account, $newCrontab);
    }

    public function updateJob(Account $account, CronJob $job): bool
    {
        // Remove old job and add new one
        $this->deleteJob($account, $job);
        return $this->createJob($account, $job);
    }

    public function deleteJob(Account $account, CronJob $job): bool
    {
        $currentCrontab = $this->getCurrentCrontab($account);
        $lines = explode("\n", $currentCrontab);
        
        // Filter out the job to delete
        $newLines = array_filter($lines, function($line) use ($job) {
            return !str_contains($line, $job->command);
        });
        
        $newCrontab = implode("\n", $newLines);
        
        return $this->writeCrontab($account, $newCrontab);
    }

    public function listJobs(Account $account): array
    {
        $crontab = $this->getCurrentCrontab($account);
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

    private function getCurrentCrontab(Account $account): string
    {
        $result = $this->shell->executeSudo('crontab', ['-u', $account->username, '-l']);
        
        if ($result['exitCode'] === 0) {
            return $result['output'];
        }
        
        return '';
    }

    private function writeCrontab(Account $account, string $content): bool
    {
        $tempFile = tempnam(sys_get_temp_dir(), 'crontab');
        file_put_contents($tempFile, $content);
        
        $result = $this->shell->executeSudo('crontab', ['-u', $account->username, $tempFile]);
        
        unlink($tempFile);
        
        return $result['exitCode'] === 0;
    }

    private function formatCronLine(CronJob $job): string
    {
        return "{$job->schedule} {$job->command}";
    }
}
