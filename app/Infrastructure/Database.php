<?php

namespace App\Infrastructure;

use PDO;
use PDOException;

class Database
{
    private static ?PDO $panelConnection = null;

    /**
     * Get the panel database connection (SQLite)
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
