<?php

require_once __DIR__ . '/../bootstrap/app.php';

use App\Facades\App;
use App\Support\AuditLogger;

$withinDays = isset($argv[1]) ? max(0, (int) $argv[1]) : 30;

try {
    $summary = App::acmeCertificates()->renewDueCertificates($withinDays);

    AuditLogger::log('certificate.renewal_job.completed', 'Scheduled certificate renewal run completed', [
        'within_days' => $withinDays,
        'summary' => $summary,
    ]);

    echo sprintf(
        "Processed: %d\nRenewed: %d\nFailed: %d\n",
        $summary['processed'],
        $summary['renewed'],
        $summary['failed']
    );

    exit($summary['failed'] > 0 ? 1 : 0);
} catch (Throwable $exception) {
    AuditLogger::log('certificate.renewal_job.failed', 'Scheduled certificate renewal run failed', [
        'within_days' => $withinDays,
        'error' => $exception->getMessage(),
    ]);

    fwrite(STDERR, $exception->getMessage() . PHP_EOL);
    exit(1);
}
