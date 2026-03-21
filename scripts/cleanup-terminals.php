#!/usr/bin/env php
<?php
/**
 * Terminal Session Cleanup Script
 *
 * Cleans up stale terminal sessions from the panel database.  A session is
 * considered stale if:
 *   1. Its TTL has expired (expires_at <= now), OR
 *   2. It has been idle for longer than the idle-timeout threshold, OR
 *   3. Its ttyd process is no longer running (orphaned record).
 *
 * Active sessions are never terminated based on age alone; the application
 * must call TerminalAdapter::updateSessionActivity() periodically.
 *
 * Usage:
 *   php scripts/cleanup-terminals.php [--max-idle=300] [--dry-run] [--verbose]
 *
 * Options:
 *   --max-idle=SECONDS  Maximum idle time in seconds (default: 300 = 5 minutes)
 *   --dry-run           Report what would be cleaned without making changes
 *   --verbose           Show detailed output
 *
 * Cron example (every 5 minutes):
 *   *\/5 * * * * cd /opt/novapanel && php scripts/cleanup-terminals.php \
 *       >> storage/logs/terminal-cleanup.log 2>&1
 */

require_once __DIR__ . '/../vendor/autoload.php';

use App\Infrastructure\Adapters\TerminalAdapter;
use App\Infrastructure\Shell\Shell;

// Parse command-line arguments
$options = [
    'max-idle' => TerminalAdapter::IDLE_TIMEOUT,
    'dry-run'  => false,
    'verbose'  => false,
];

foreach (array_slice($argv, 1) as $arg) {
    if (strpos($arg, '--max-idle=') === 0) {
        $options['max-idle'] = (int) substr($arg, 11);
    } elseif ($arg === '--dry-run') {
        $options['dry-run'] = true;
    } elseif ($arg === '--verbose' || $arg === '-v') {
        $options['verbose'] = true;
    }
}

if ($options['max-idle'] < 60) {
    echo "Error: --max-idle must be at least 60 seconds\n";
    exit(1);
}

$shell   = new Shell();
$adapter = new TerminalAdapter($shell);

$timestamp = date('Y-m-d H:i:s');

if ($options['verbose']) {
    echo "[{$timestamp}] Terminal cleanup starting...\n";
    echo "Max idle time : {$options['max-idle']}s (" . ($options['max-idle'] / 60) . " min)\n";
    echo "Session TTL   : " . TerminalAdapter::SESSION_TTL . "s (" . (TerminalAdapter::SESSION_TTL / 60) . " min)\n";
    if ($options['dry-run']) {
        echo "DRY RUN MODE  – no changes will be made\n";
    }
    echo "\n";
}

$activeBefore = count($adapter->getActiveSessions());
if ($options['verbose']) {
    echo "Active sessions before cleanup: {$activeBefore}\n\n";
}

$cleaned = 0;
if (!$options['dry-run']) {
    $cleaned = $adapter->cleanupStaleSessions($options['max-idle']);
}

$timestamp = date('Y-m-d H:i:s');
if ($cleaned > 0) {
    $verb = $options['dry-run'] ? 'Would clean' : 'Cleaned';
    echo "[{$timestamp}] {$verb} {$cleaned} stale terminal session(s)\n";
} elseif ($options['verbose']) {
    echo "[{$timestamp}] No stale sessions found\n";
}

exit(0);

