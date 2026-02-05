# Control Panel (Single VPS) – System Design

## 1. Project Summary
A lightweight, open-source **single VPS control panel** built with:
- **PHP classes** (backend logic)
- **HTML, CSS, JS** (frontend)
- **SQLite** (panel DB)
- **Nginx** + **PHP-FPM** (hosting stack)
- **BIND9** (authoritative DNS with zone files)
- **Pure-FTPd** (FTP with virtual users)
- **MySQL/MariaDB/Postgres** (customer databases)
- **Cron** (per panel user)
- **Role-based access control**

**Single VPS Design - ONE Linux User Model:**
- **CRITICAL:** Only ONE Linux system user exists: `novapanel`
- All sites, services, and processes run as `novapanel`
- Panel users are database records ONLY (NOT Linux users)
- Panel users own and manage their sites through the web interface
- All sites stored under `/opt/novapanel/sites/{panel_username}/`
- No separate Linux system accounts are ever created

No SaaS, no multi-node cluster, no email server, no separate Linux accounts.

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
     - CreateFtpUserService (virtual FTP users only)
     - SetupDnsZoneService
     - AssignPhpVersionService
     - CreateDatabaseService
     - AddCronJobService
   - Calls services from façade layer
   - **Note:** No CreateAccountService - panel users are database records

3. **Domain Layer**
   - Entities:
     - User, Role, Permission (panel access control)
     - Site, Domain (websites)
     - FtpUser, CronJob, Database (resources)
   - Domain rules & validation

4. **Infrastructure Layer**
   - Shell runner
   - Nginx adapter
   - BIND9 adapter
   - Pure-FTPd adapter
   - PHP-FPM adapter
   - MySQL adapter
   - Cron adapter
   - SQLite storage

---

## 3. Data Storage Strategy

### 3.1 Panel DB (SQLite)
Stores panel metadata:
- users, roles, permissions (panel users and their access control)
- sites, domains (websites linked directly to panel users)
- ftp_users (FTP access linked to panel users)
- cron_jobs (scheduled tasks linked to panel users)
- databases (MySQL databases linked to panel users)

### 3.2 Service Databases
- **MySQL/MariaDB**
  - Customer DBs only (PostgreSQL not installed by default)
- **BIND9 Zone Files**
  - DNS zones/records stored in `/etc/bind/zones/`
  - Complete isolation from database access
- **System-level files**
  - PHP-FPM pool configs (all run as `novapanel` user)
  - Nginx vhosts
  - Cron jobs (all in `novapanel` user's crontab)

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

public function createJob(User $panelUser, CronJob $job): bool;


---

5. Core Features

5.1 Hosting

Nginx vhosts (all served by `novapanel` user)

PHP-FPM multi-version pools (all run as `novapanel` user)

Per-site PHP version

Directory organization by panel user (all owned by `novapanel:novapanel`)


5.2 DNS (BIND9)

Create zone (zone files stored in /etc/bind/zones)

Add/edit/delete A, AAAA, CNAME, TXT, MX, SRV records

Complete isolation from database access for enhanced security


5.3 FTP (Pure-FTPd)

**Virtual Users Only:**
- Create/delete FTP users via Pure-FTPd virtual users
- All FTP users map to the `novapanel` Linux user's UID/GID
- **NEVER create Linux system users for FTP**
- FTP users are jailed to their designated directories

5.4 Databases

Create DB (MySQL/Postgres)

Create DB users

Assign privileges


5.5 Cron

Per panel user scheduled tasks

All jobs written to `novapanel` user's crontab

Jobs identified by comment tags showing panel user ownership


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

**Single Linux User Architecture:**
- Panel runs as non-root `novapanel` user
- **NO Linux user creation/modification/deletion allowed**
- All operations run as `novapanel`

Controlled sudo access (minimal):
- systemd reload (for nginx, php-fpm)
- writing config files to system directories
- pure-pw (for FTP virtual users)

**Explicitly FORBIDDEN:**
- useradd/usermod/userdel commands
- Creating additional Linux system users
- Any operation that would create system accounts




---

8. Lifecycles

Create Site Flow

1. Validate panel user exists in database
2. Create directory structure under `/opt/novapanel/sites/{panel_username}/{domain}/`
3. Set ownership to `novapanel:novapanel`
4. Assign PHP runtime version
5. Generate FPM pool (user = novapanel, group = novapanel)
6. Generate Nginx vhost
7. Reload Nginx
8. Create DNS zone (optional)
9. Create default DNS records (optional)
10. Create FTP virtual user (optional, maps to novapanel UID)
11. Create database (optional)
12. Flush caches

**Important:** All files owned by `novapanel`, all processes run as `novapanel`



---

9. Future Extensions

Mail server module

Backup service

API layer

Plugin system

Agent mode


---
