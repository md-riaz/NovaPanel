<?php

namespace App\Facades;

use App\Repositories\UserRepository;
use App\Repositories\SiteRepository;
use App\Repositories\DatabaseRepository;
use App\Repositories\DatabaseUserRepository;
use App\Repositories\FtpUserRepository;
use App\Repositories\CronJobRepository;
use App\Repositories\DomainRepository;
use App\Repositories\DnsRecordRepository;
use App\Repositories\RoleRepository;
use App\Services\CreateSiteService;
use App\Services\CreateDatabaseService;
use App\Services\CreateFtpUserService;
use App\Services\AddCronJobService;
use App\Services\SetupDnsZoneService;
use App\Infrastructure\Shell\Shell;

/**
 * Application Facade
 * 
 * Provides centralized access to commonly used repositories and services.
 * This facade implements the Singleton pattern for repositories and factory pattern for services.
 * 
 * Usage Examples:
 * - $users = App::users()->all();
 * - $site = App::createSiteService()->execute(...);
 */
class App
{
    // Repository instances (Singleton)
    private static ?UserRepository $userRepository = null;
    private static ?SiteRepository $siteRepository = null;
    private static ?DatabaseRepository $databaseRepository = null;
    private static ?DatabaseUserRepository $databaseUserRepository = null;
    private static ?FtpUserRepository $ftpUserRepository = null;
    private static ?CronJobRepository $cronJobRepository = null;
    private static ?DomainRepository $domainRepository = null;
    private static ?DnsRecordRepository $dnsRecordRepository = null;
    private static ?RoleRepository $roleRepository = null;
    private static ?Shell $shell = null;

    /**
     * Get UserRepository instance
     */
    public static function users(): UserRepository
    {
        if (self::$userRepository === null) {
            self::$userRepository = new UserRepository();
        }
        return self::$userRepository;
    }

    /**
     * Get SiteRepository instance
     */
    public static function sites(): SiteRepository
    {
        if (self::$siteRepository === null) {
            self::$siteRepository = new SiteRepository();
        }
        return self::$siteRepository;
    }

    /**
     * Get DatabaseRepository instance
     */
    public static function databases(): DatabaseRepository
    {
        if (self::$databaseRepository === null) {
            self::$databaseRepository = new DatabaseRepository();
        }
        return self::$databaseRepository;
    }

    /**
     * Get DatabaseUserRepository instance
     */
    public static function databaseUsers(): DatabaseUserRepository
    {
        if (self::$databaseUserRepository === null) {
            self::$databaseUserRepository = new DatabaseUserRepository();
        }
        return self::$databaseUserRepository;
    }

    /**
     * Get FtpUserRepository instance
     */
    public static function ftpUsers(): FtpUserRepository
    {
        if (self::$ftpUserRepository === null) {
            self::$ftpUserRepository = new FtpUserRepository();
        }
        return self::$ftpUserRepository;
    }

    /**
     * Get CronJobRepository instance
     */
    public static function cronJobs(): CronJobRepository
    {
        if (self::$cronJobRepository === null) {
            self::$cronJobRepository = new CronJobRepository();
        }
        return self::$cronJobRepository;
    }

    /**
     * Get DomainRepository instance
     */
    public static function domains(): DomainRepository
    {
        if (self::$domainRepository === null) {
            self::$domainRepository = new DomainRepository();
        }
        return self::$domainRepository;
    }

    /**
     * Get DnsRecordRepository instance
     */
    public static function dnsRecords(): DnsRecordRepository
    {
        if (self::$dnsRecordRepository === null) {
            self::$dnsRecordRepository = new DnsRecordRepository();
        }
        return self::$dnsRecordRepository;
    }

    /**
     * Get RoleRepository instance
     */
    public static function roles(): RoleRepository
    {
        if (self::$roleRepository === null) {
            self::$roleRepository = new RoleRepository();
        }
        return self::$roleRepository;
    }

    /**
     * Get Shell instance
     */
    public static function shell(): Shell
    {
        if (self::$shell === null) {
            self::$shell = new Shell();
        }
        return self::$shell;
    }

    /**
     * Create CreateSiteService instance (Factory method)
     * Services are created fresh each time to avoid state issues
     */
    public static function createSiteService(): CreateSiteService
    {
        return new CreateSiteService(
            self::sites(),
            self::users(),
            WebServer::getInstance(),
            PhpRuntime::getInstance(),
            self::shell()
        );
    }

    /**
     * Create CreateDatabaseService instance (Factory method)
     */
    public static function createDatabaseService(): CreateDatabaseService
    {
        return new CreateDatabaseService(
            self::databases(),
            self::databaseUsers(),
            self::users(),
            DatabaseManager::getInstance()
        );
    }

    /**
     * Create CreateFtpUserService instance (Factory method)
     */
    public static function createFtpUserService(): CreateFtpUserService
    {
        return new CreateFtpUserService(
            self::ftpUsers(),
            self::users(),
            Ftp::getInstance()
        );
    }

    /**
     * Create AddCronJobService instance (Factory method)
     */
    public static function addCronJobService(): AddCronJobService
    {
        return new AddCronJobService(
            self::cronJobs(),
            self::users(),
            Cron::getInstance()
        );
    }

    /**
     * Create SetupDnsZoneService instance (Factory method)
     */
    public static function setupDnsZoneService(): SetupDnsZoneService
    {
        return new SetupDnsZoneService(
            self::domains(),
            self::dnsRecords(),
            self::sites(),
            Dns::getInstance()
        );
    }
}
