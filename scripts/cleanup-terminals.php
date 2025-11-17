#!/usr/bin/env php
<?php
/**
 * Terminal Session Cleanup Script
 * 
 * This script cleans up stale terminal sessions. A session is considered stale if:
 * 1. The ttyd process is not running anymore (orphaned session files), OR
 * 2. The session has been inactive for longer than the max-idle threshold
 * 
 * Active sessions (where ttyd process is still running) are NOT terminated based on 
 * age alone. The application should call TerminalAdapter::updateSessionActivity() 
 * periodically to track activity. Without activity updates, sessions will be 
 * considered idle after max-idle seconds.
 * 
 * Should be run periodically via cron (e.g., every hour).
 * 
 * Usage:
 *   php scripts/cleanup-terminals.php [--max-idle=3600]
 * 
 * Options:
 *   --max-idle=SECONDS   Maximum idle time in seconds (default: 3600 = 1 hour)
 *   --dry-run           Show what would be cleaned up without actually doing it
 *   --verbose           Show detailed output
 * 
 * Example cron entry (run every hour):
 *   0 * * * * cd /opt/novapanel && php scripts/cleanup-terminals.php >> storage/logs/terminal-cleanup.log 2>&1
 */

// Load autoloader
require_once __DIR__ . '/../vendor/autoload.php';

use App\Infrastructure\Adapters\TerminalAdapter;
use App\Infrastructure\Shell\Shell;

// Parse command line arguments
$options = [
    'max-idle' => 3600,  // 1 hour default
    'dry-run' => false,
    'verbose' => false,
];

foreach ($argv as $arg) {
    if (strpos($arg, '--max-idle=') === 0) {
        $options['max-idle'] = (int) substr($arg, 11);
    } elseif ($arg === '--dry-run') {
        $options['dry-run'] = true;
    } elseif ($arg === '--verbose' || $arg === '-v') {
        $options['verbose'] = true;
    }
}

// Validate options
if ($options['max-idle'] < 60) {
    echo "Error: max-idle must be at least 60 seconds\n";
    exit(1);
}

// Create adapter
$shell = new Shell();
$adapter = new TerminalAdapter($shell);

// Get current time
$timestamp = date('Y-m-d H:i:s');

if ($options['verbose']) {
    echo "[{$timestamp}] Terminal cleanup starting...\n";
    echo "Max idle time: {$options['max-idle']} seconds (" . ($options['max-idle'] / 60) . " minutes)\n";
    if ($options['dry-run']) {
        echo "DRY RUN MODE - No changes will be made\n";
    }
    echo "\n";
}

// Get active sessions before cleanup
$activeBefore = $adapter->getActiveSessions();
if ($options['verbose']) {
    echo "Active sessions before cleanup: " . count($activeBefore) . "\n";
    if (!empty($activeBefore)) {
        echo "Active user IDs: " . implode(', ', $activeBefore) . "\n";
    }
    echo "\n";
}

// Perform cleanup (unless dry-run)
$cleaned = 0;
if (!$options['dry-run']) {
    $cleaned = $adapter->cleanupStaleSessions($options['max-idle']);
} else {
    // In dry-run mode, manually check what would be cleaned
    $pidDir = __DIR__ . '/../storage/terminal/pids';
    $files = glob($pidDir . '/*.json');
    
    foreach ($files as $file) {
        $content = @file_get_contents($file);
        if ($content === false) {
            continue;
        }
        
        $info = json_decode($content, true);
        if (!$info || !isset($info['user_id']) || !isset($info['created_at'])) {
            continue;
        }
        
        $userId = $info['user_id'];
        $age = time() - $info['created_at'];
        
        if ($age > $options['max-idle']) {
            $cleaned++;
            if ($options['verbose']) {
                echo "Would clean up session for user {$userId} (age: {$age}s)\n";
            }
        }
    }
}

// Get active sessions after cleanup
if (!$options['dry-run']) {
    $activeAfter = $adapter->getActiveSessions();
    
    if ($options['verbose']) {
        echo "Active sessions after cleanup: " . count($activeAfter) . "\n";
        if (!empty($activeAfter)) {
            echo "Active user IDs: " . implode(', ', $activeAfter) . "\n";
        }
        echo "\n";
    }
}

// Report results
$timestamp = date('Y-m-d H:i:s');
if ($cleaned > 0) {
    $action = $options['dry-run'] ? 'Would clean' : 'Cleaned';
    echo "[{$timestamp}] {$action} {$cleaned} stale terminal session(s)\n";
} else {
    if ($options['verbose']) {
        echo "[{$timestamp}] No stale sessions found\n";
    }
}

exit(0);
