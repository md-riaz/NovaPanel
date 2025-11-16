# Control Panel (Single VPS) – System Design

## 1. Project Summary
A lightweight, open-source **single VPS control panel** built with:
- **PHP classes** (backend logic)
- **HTML, CSS, JS** (frontend)
- **SQLite** (panel DB)
- **Nginx** + **PHP-FPM** (hosting stack)
- **PowerDNS** (authoritative DNS)
- **Pure-FTPd** (FTP)
- **MySQL/MariaDB/Postgres** (customer databases)
- **Cron** (per account)
- **Role-based access control**

No SaaS, no multi-node cluster, no email server.

Architected with **facades + adapters** and a strict layered pattern.

---

## 2. Architecture Overview

### 2.1 Layers
1. **HTTP Layer**
   - Router, middleware, controllers
   - Render HTML templates
   - Map HTTP requests → application services

2. **Application Layer**
   - Use-cases:
     - CreateSiteService
     - CreateAccountService
     - CreateFtpUserService
     - SetupDnsZoneService
     - AssignPhpVersionService
     - CreateDatabaseService
     - AddCronJobService
   - Calls services from façade layer

3. **Domain Layer**
   - Entities:
     - User, Role, Permission
     - Account, Site, Domain
     - FtpUser, CronJob, Database
   - Domain rules & validation

4. **Infrastructure Layer**
   - Shell runner
   - Nginx adapter
   - PowerDNS adapter
   - Pure-FTPd adapter
   - PHP-FPM adapter
   - MySQL adapter
   - Cron adapter
   - SQLite storage

---

## 3. Data Storage Strategy

### 3.1 Panel DB (SQLite)
Stores panel metadata:
- users, roles, permissions
- accounts, sites, domains
- ftp_users (optional shadow)
- cron_jobs (panel-side)
- database_credentials (for customer DBs)

### 3.2 Service Databases
- **MySQL/MariaDB/Postgres**
  - Customer DBs only
- **PowerDNS MySQL backend**
  - PDNS-managed zones/records
- **System-level files**
  - PHP-FPM pool configs
  - Nginx vhosts
  - Cron jobs (per system user)

---

## 4. Facade Contracts

### WebServerManager
```php
public function createSite(Site $site): bool;
public function updateSite(Site $site): bool;
public function deleteSite(Site $site): bool;

DnsManager

public function createZone(Domain $domain): bool;
public function deleteZone(Domain $domain): bool;
public function addRecord(DnsRecord $record): bool;

FtpManager

public function createUser(FtpUser $user, string $password): bool;
public function deleteUser(FtpUser $user): bool;

PhpRuntimeManager

public function listAvailable(): array;
public function assignRuntimeToSite(Site $site, PhpRuntime $runtime): bool;

CronManager

public function createJob(Account $account, CronJob $job): bool;


---

5. Core Features

5.1 Hosting

Nginx vhosts

PHP-FPM multi-version pools

Per-site PHP version

Per-account directory isolation


5.2 DNS (PowerDNS)

Create zone

Add/edit/delete A, AAAA, CNAME, TXT, MX, SRV records


5.3 FTP (Pure-FTPd)

Option A: System users
Create/delete FTP users via Linux users.

5.4 Databases

Create DB (MySQL/Postgres)

Create DB users

Assign privileges


5.5 Cron

Per-account scheduled tasks

Written to system crontab


5.6 File Manager (Basic)

Browse files

Upload/download/edit


5.7 RBAC

Roles:

Admin

Account Owner

Developer

ReadOnly



---

6. Directory Structure

project/
├─ public/
├─ app/
│  ├─ Http/
│  ├─ Domain/
│  ├─ Services/
│  ├─ Contracts/
│  ├─ Facades/
│  ├─ Infrastructure/
│  ├─ Support/
├─ resources/
├─ storage/
└─ config/


---

7. Security Model

Panel runs as non-root panel user

Controlled sudo access:

systemd reload

useradd/usermod/userdel

writing config files




---

8. Lifecycles

Create Site Flow

Validate account quotas

Create directory structure

Assign PHP runtime

Generate FPM pool

Generate Nginx vhost

Reload Nginx

Create DNS zone

Create default records

Optional: create FTP user

Optional: create DB

Flush caches



---

9. Future Extensions

Mail server module

Backup service

API layer

Plugin system

Agent mode


---
