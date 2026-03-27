<?php

namespace App\Facades;

use App\Infrastructure\Shell\Shell;
use App\Repositories\CronJobRepository;
use App\Repositories\DatabaseRepository;
use App\Repositories\DatabaseUserRepository;
use App\Repositories\DnsRecordRepository;
use App\Repositories\DomainRepository;
use App\Repositories\FtpUserRepository;
use App\Repositories\RoleRepository;
use App\Repositories\SiteRepository;
use App\Repositories\TerminalSessionRepository;
use App\Repositories\UserRepository;
use App\Services\AcmeCertificateService;
use App\Services\AddCronJobService;
use App\Services\CreateDatabaseService;
use App\Services\CreateFtpUserService;
use App\Services\CreateSiteService;
use App\Services\SetupDnsZoneService;
use App\Services\SiteHttpsService;

class App
{
    private static ?UserRepository $userRepository = null;
    private static ?SiteRepository $siteRepository = null;
    private static ?DatabaseRepository $databaseRepository = null;
    private static ?DatabaseUserRepository $databaseUserRepository = null;
    private static ?FtpUserRepository $ftpUserRepository = null;
    private static ?CronJobRepository $cronJobRepository = null;
    private static ?DomainRepository $domainRepository = null;
    private static ?DnsRecordRepository $dnsRecordRepository = null;
    private static ?RoleRepository $roleRepository = null;
    private static ?TerminalSessionRepository $terminalSessionRepository = null;
    private static ?Shell $shell = null;
    private static ?AcmeCertificateService $acmeCertificateService = null;
    private static ?SiteHttpsService $siteHttpsService = null;

    public static function users(): UserRepository
    {
        if (self::$userRepository === null) {
            self::$userRepository = new UserRepository();
        }

        return self::$userRepository;
    }

    public static function sites(): SiteRepository
    {
        if (self::$siteRepository === null) {
            self::$siteRepository = new SiteRepository();
        }

        return self::$siteRepository;
    }

    public static function databases(): DatabaseRepository
    {
        if (self::$databaseRepository === null) {
            self::$databaseRepository = new DatabaseRepository();
        }

        return self::$databaseRepository;
    }

    public static function databaseUsers(): DatabaseUserRepository
    {
        if (self::$databaseUserRepository === null) {
            self::$databaseUserRepository = new DatabaseUserRepository();
        }

        return self::$databaseUserRepository;
    }

    public static function ftpUsers(): FtpUserRepository
    {
        if (self::$ftpUserRepository === null) {
            self::$ftpUserRepository = new FtpUserRepository();
        }

        return self::$ftpUserRepository;
    }

    public static function cronJobs(): CronJobRepository
    {
        if (self::$cronJobRepository === null) {
            self::$cronJobRepository = new CronJobRepository();
        }

        return self::$cronJobRepository;
    }

    public static function domains(): DomainRepository
    {
        if (self::$domainRepository === null) {
            self::$domainRepository = new DomainRepository();
        }

        return self::$domainRepository;
    }

    public static function dnsRecords(): DnsRecordRepository
    {
        if (self::$dnsRecordRepository === null) {
            self::$dnsRecordRepository = new DnsRecordRepository();
        }

        return self::$dnsRecordRepository;
    }

    public static function roles(): RoleRepository
    {
        if (self::$roleRepository === null) {
            self::$roleRepository = new RoleRepository();
        }

        return self::$roleRepository;
    }

    public static function terminalSessions(): TerminalSessionRepository
    {
        if (self::$terminalSessionRepository === null) {
            self::$terminalSessionRepository = new TerminalSessionRepository();
        }

        return self::$terminalSessionRepository;
    }

    public static function shell(): Shell
    {
        if (self::$shell === null) {
            self::$shell = new Shell();
        }

        return self::$shell;
    }

    public static function acmeCertificates(): AcmeCertificateService
    {
        if (self::$acmeCertificateService === null) {
            self::$acmeCertificateService = new AcmeCertificateService(
                self::sites(),
                WebServer::getInstance(),
                self::shell()
            );
        }

        return self::$acmeCertificateService;
    }

    public static function siteHttpsService(): SiteHttpsService
    {
        if (self::$siteHttpsService === null) {
            self::$siteHttpsService = new SiteHttpsService(
                self::sites(),
                WebServer::getInstance()
            );
        }

        return self::$siteHttpsService;
    }

    public static function createSiteService(): CreateSiteService
    {
        return new CreateSiteService(
            self::sites(),
            self::users(),
            WebServer::getInstance(),
            PhpRuntime::getInstance(),
            self::shell(),
            self::acmeCertificates()
        );
    }

    public static function createDatabaseService(): CreateDatabaseService
    {
        return new CreateDatabaseService(
            self::databases(),
            self::databaseUsers(),
            self::users(),
            DatabaseManager::getInstance()
        );
    }

    public static function createFtpUserService(): CreateFtpUserService
    {
        return new CreateFtpUserService(
            self::ftpUsers(),
            self::users(),
            Ftp::getInstance()
        );
    }

    public static function addCronJobService(): AddCronJobService
    {
        return new AddCronJobService(
            self::cronJobs(),
            self::users(),
            Cron::getInstance()
        );
    }

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
