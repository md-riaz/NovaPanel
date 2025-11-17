<?php

namespace App\Support;

use App\Http\Session;

class AuditLogger
{
    private const LOG_FILE = __DIR__ . '/../../storage/logs/audit.log';
    
    /**
     * Log an audit event
     */
    public static function log(string $action, string $message, array $context = []): void
    {
        // Ensure log directory exists
        $logDir = dirname(self::LOG_FILE);
        if (!is_dir($logDir)) {
            @mkdir($logDir, 0750, true);
        }
        
        // Get current user info
        Session::start();
        $userId = Session::get('user_id', 'unknown');
        $username = Session::get('username', 'unknown');
        
        // Add user info to context
        $context['user_id'] = $userId;
        $context['username'] = $username;
        $context['ip'] = $_SERVER['REMOTE_ADDR'] ?? 'unknown';
        
        // Format log message
        $timestamp = date('Y-m-d H:i:s');
        $contextJson = json_encode($context);
        
        $logMessage = sprintf(
            "[%s] ACTION=%s MESSAGE=%s CONTEXT=%s\n",
            $timestamp,
            $action,
            $message,
            $contextJson
        );
        
        // Write to log file
        @file_put_contents(self::LOG_FILE, $logMessage, FILE_APPEND);
    }
    
    /**
     * Log resource creation
     */
    public static function logCreated(string $resource, string $name, array $details = []): void
    {
        self::log(
            "{$resource}.created",
            "{$resource} '{$name}' was created",
            array_merge(['resource' => $resource, 'name' => $name], $details)
        );
    }
    
    /**
     * Log resource update
     */
    public static function logUpdated(string $resource, string $name, array $details = []): void
    {
        self::log(
            "{$resource}.updated",
            "{$resource} '{$name}' was updated",
            array_merge(['resource' => $resource, 'name' => $name], $details)
        );
    }
    
    /**
     * Log resource deletion
     */
    public static function logDeleted(string $resource, string $name, array $details = []): void
    {
        self::log(
            "{$resource}.deleted",
            "{$resource} '{$name}' was deleted",
            array_merge(['resource' => $resource, 'name' => $name], $details)
        );
    }
    
    /**
     * Log authentication events
     */
    public static function logAuth(string $event, string $username, bool $success = true): void
    {
        self::log(
            "auth.{$event}",
            "Authentication {$event} for user '{$username}' " . ($success ? 'succeeded' : 'failed'),
            ['username' => $username, 'success' => $success]
        );
    }
}
