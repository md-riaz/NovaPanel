<?php

namespace App\Infrastructure;

use PDO;
use PDOException;

/**
 * Database Connection Manager
 * 
 * IMPORTANT: NovaPanel uses SQLite for ALL panel operations.
 * - Panel database (SQLite): stores users, sites, permissions, DNS records, FTP users, cron jobs, etc.
 * - MySQL/PostgreSQL: ONLY used for creating customer databases for their websites
 * 
 * The panel itself does NOT use MySQL or PostgreSQL for its own data.
 */
class Database
{
    private static ?PDO $panelConnection = null;

    /**
     * Get the panel database connection (SQLite)
     * 
     * This is the ONLY database used for panel operations.
     * All panel data (users, sites, permissions, etc.) is stored in SQLite.
     * 
     * @return PDO SQLite database connection
     */
    public static function panel(): PDO
    {
        if (self::$panelConnection === null) {
            $dbPath = __DIR__ . '/../../storage/panel.db';
            
            try {
                self::$panelConnection = new PDO("sqlite:$dbPath");
                self::$panelConnection->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
                self::$panelConnection->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC);
            } catch (PDOException $e) {
                throw new \RuntimeException("Failed to connect to panel database: " . $e->getMessage());
            }
        }

        return self::$panelConnection;
    }

    /**
     * Get MySQL connection for customer databases
     * 
     * This is ONLY used for creating and managing customer databases.
     * The panel itself does NOT use MySQL for its operations.
     * 
     * @param string $host MySQL server hostname
     * @param string $database Database name
     * @param string $username MySQL username
     * @param string $password MySQL password
     * @return PDO MySQL database connection
     */
    public static function mysql(string $host, string $database, string $username, string $password): PDO
    {
        try {
            $pdo = new PDO("mysql:host=$host;dbname=$database", $username, $password);
            $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
            $pdo->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC);
            return $pdo;
        } catch (PDOException $e) {
            throw new \RuntimeException("Failed to connect to MySQL database: " . $e->getMessage());
        }
    }

    /**
     * Get PostgreSQL connection for customer databases
     * 
     * This is ONLY used for creating and managing customer databases.
     * The panel itself does NOT use PostgreSQL for its operations.
     * 
     * @param string $host PostgreSQL server hostname
     * @param string $database Database name
     * @param string $username PostgreSQL username
     * @param string $password PostgreSQL password
     * @return PDO PostgreSQL database connection
     */
    public static function postgres(string $host, string $database, string $username, string $password): PDO
    {
        try {
            $pdo = new PDO("pgsql:host=$host;dbname=$database", $username, $password);
            $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
            $pdo->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC);
            return $pdo;
        } catch (PDOException $e) {
            throw new \RuntimeException("Failed to connect to PostgreSQL database: " . $e->getMessage());
        }
    }
}
