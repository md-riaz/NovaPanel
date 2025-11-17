<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\DatabaseManagerInterface;
use App\Contracts\ShellInterface;
use App\Domain\Entities\Database;
use App\Domain\Entities\DatabaseUser;
use PDO;

/**
 * MySQL Database Adapter - manages MySQL databases and users
 */
class MysqlDatabaseAdapter implements DatabaseManagerInterface
{
    private ?PDO $mysqlRoot = null;

    public function __construct(
        private ShellInterface $shell,
        private string $host = 'localhost',
        private string $rootUser = 'root',
        private string $rootPassword = ''
    ) {}

    public function createDatabase(Database $database): bool
    {
        try {
            $db = $this->getRootConnection();
            
            // Sanitize database name to prevent SQL injection
            $dbName = $this->sanitizeDatabaseName($database->name);
            
            // Create database
            $stmt = $db->prepare("CREATE DATABASE IF NOT EXISTS `{$dbName}` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci");
            $stmt->execute();
            
            return true;
        } catch (\PDOException $e) {
            error_log("MySQL database creation failed: " . $e->getMessage());
            return false;
        }
    }

    public function deleteDatabase(Database $database): bool
    {
        try {
            $db = $this->getRootConnection();
            
            $dbName = $this->sanitizeDatabaseName($database->name);
            
            // Drop database
            $stmt = $db->prepare("DROP DATABASE IF EXISTS `{$dbName}`");
            $stmt->execute();
            
            return true;
        } catch (\PDOException $e) {
            error_log("MySQL database deletion failed: " . $e->getMessage());
            return false;
        }
    }

    public function createUser(DatabaseUser $user, string $password): bool
    {
        try {
            $db = $this->getRootConnection();
            
            $username = $this->sanitizeUsername($user->username);
            $host = $user->host ?? 'localhost';
            
            // Create user
            $stmt = $db->prepare("CREATE USER IF NOT EXISTS ?@? IDENTIFIED BY ?");
            $stmt->execute([$username, $host, $password]);
            
            // Flush privileges
            $db->exec("FLUSH PRIVILEGES");
            
            return true;
        } catch (\PDOException $e) {
            error_log("MySQL user creation failed: " . $e->getMessage());
            return false;
        }
    }

    public function deleteUser(DatabaseUser $user): bool
    {
        try {
            $db = $this->getRootConnection();
            
            $username = $this->sanitizeUsername($user->username);
            $host = $user->host ?? 'localhost';
            
            // Drop user
            $stmt = $db->prepare("DROP USER IF EXISTS ?@?");
            $stmt->execute([$username, $host]);
            
            // Flush privileges
            $db->exec("FLUSH PRIVILEGES");
            
            return true;
        } catch (\PDOException $e) {
            error_log("MySQL user deletion failed: " . $e->getMessage());
            return false;
        }
    }

    public function grantPrivileges(DatabaseUser $user, Database $database, array $privileges): bool
    {
        try {
            $db = $this->getRootConnection();
            
            $username = $this->sanitizeUsername($user->username);
            $host = $user->host ?? 'localhost';
            $dbName = $this->sanitizeDatabaseName($database->name);
            
            // Default to all privileges if none specified
            if (empty($privileges)) {
                $privileges = ['ALL PRIVILEGES'];
            }
            
            $privilegesStr = implode(', ', $privileges);
            
            // Grant privileges
            $sql = "GRANT {$privilegesStr} ON `{$dbName}`.* TO ?@?";
            $stmt = $db->prepare($sql);
            $stmt->execute([$username, $host]);
            
            // Flush privileges
            $db->exec("FLUSH PRIVILEGES");
            
            return true;
        } catch (\PDOException $e) {
            error_log("MySQL privilege grant failed: " . $e->getMessage());
            return false;
        }
    }

    private function getRootConnection(): PDO
    {
        if ($this->mysqlRoot === null) {
            try {
                // Validate credentials are provided
                if (empty($this->rootUser)) {
                    throw new \RuntimeException("MySQL root user is not configured. Please set MYSQL_ROOT_USER in .env.php");
                }
                
                $this->mysqlRoot = new PDO(
                    "mysql:host={$this->host}",
                    $this->rootUser,
                    $this->rootPassword
                );
                $this->mysqlRoot->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
            } catch (\PDOException $e) {
                throw new \RuntimeException("Failed to connect to MySQL as root: " . $e->getMessage() . ". Please verify MYSQL_ROOT_USER and MYSQL_ROOT_PASSWORD in .env.php are correct.");
            }
        }
        
        return $this->mysqlRoot;
    }

    /**
     * Sanitize database name to prevent SQL injection
     * Database names can only contain alphanumeric characters and underscores
     */
    private function sanitizeDatabaseName(string $name): string
    {
        // Remove any characters that aren't alphanumeric or underscore
        $sanitized = preg_replace('/[^a-zA-Z0-9_]/', '', $name);
        
        // Ensure name doesn't start with a number
        if (preg_match('/^[0-9]/', $sanitized)) {
            $sanitized = 'db_' . $sanitized;
        }
        
        // Limit length to 64 characters (MySQL limit)
        return substr($sanitized, 0, 64);
    }

    /**
     * Sanitize username to prevent SQL injection
     */
    private function sanitizeUsername(string $username): string
    {
        // Remove any characters that aren't alphanumeric or underscore
        $sanitized = preg_replace('/[^a-zA-Z0-9_]/', '', $username);
        
        // Limit length to 32 characters (MySQL limit)
        return substr($sanitized, 0, 32);
    }
}
