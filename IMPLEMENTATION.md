# Implementation Plan (Checkpoints for AI Agents)

This plan is structured into stages and checkpoints so an AI agent can verify tasks before moving forward.

---

## ⚠️ CRITICAL: Single Linux User Architecture

**ONLY ONE Linux system user exists: `novapanel`**

### What This Means:

✅ **DO:**
- Create panel users as database records in SQLite
- Run all PHP-FPM pools as `novapanel` user
- Run all cron jobs as `novapanel` user
- Create FTP virtual users mapping to `novapanel` UID
- Own all files as `novapanel:novapanel`

❌ **DO NOT:**
- Create Linux system users with useradd/adduser
- Use usermod or userdel commands
- Create separate system accounts for panel users
- Use system-level FTP users

### Verification:
```bash
# Only ONE user should exist for the panel
ls -la /opt/novapanel/sites/  # All owned by novapanel:novapanel
ps aux | grep php-fpm          # All pools run as novapanel
crontab -u novapanel -l        # All cron jobs here
pure-pw list                   # FTP users map to novapanel UID
```

---

## Phase 1 — Core Foundation (Must Complete First)

### 1.1 Setup Project Structure
✓ Create `project/`  
✓ Add directories: `app`, `public`, `resources`, `storage`, `config`  
✓ Initialize Composer autoloading  

**Checkpoint Validation:**
- Can load a sample controller through `public/index.php`.

---

## Phase 2 — Panel Database (SQLite)

### 2.1 Create SQLite DB file
✓ `storage/panel.db`

### 2.2 Implement Schema Migration Script
✓ Create tables:  
- users (panel users - NOT Linux users)
- roles  
- permissions  
- user_roles  
- sites (linked to panel users via user_id)
- domains  
- ftp_users (Pure-FTPd virtual users - NOT Linux users)
- cron_jobs (linked to panel users via user_id)
- databases (linked to panel users via user_id)

**Note:** No `accounts` table - single VPS model uses panel users directly  

### 2.3 Create Database Class
✓ `Database::panel()` returns PDO for SQLite

**Checkpoint Validation:**
- Run migration script → all tables created successfully.

---

## Phase 3 — HTTP Layer + MVC Framework

### 3.1 Router
✓ GET, POST routing  
✓ Controller resolution  

### 3.2 Request & Response Classes
✓ Request parsing  
✓ JSON/HTML response  

### 3.3 Middleware Engine
✓ Auth middleware  
✓ Permission middleware  

**Checkpoint Validation:**
- Can load a protected route and redirect to login.

---

## Phase 4 — Authentication + RBAC

### 4.1 User Model & Repo
✓ Create user  
✓ Check password  
✓ Load roles  

### 4.2 Role & Permission Models
✓ Assign role → permission mapping  

### 4.3 PermissionChecker
✓ Enforce per-route permission set  

**Checkpoint Validation:**
- Role with no permission cannot access `/sites`.

---

## Phase 5 — Panel User Management

### 5.1 User CRUD Operations
✓ Create panel users with roles
✓ Edit panel users  
✓ Delete panel users  
✓ Assign/remove roles  

**Checkpoint Validation:**
- Panel user can be created with Admin/AccountOwner/Developer/ReadOnly roles
- Sites can be assigned to panel users
- **Verify:** Panel user is a database record, NOT a Linux system user

**Critical:** Single VPS model - only ONE Linux user (`novapanel`) exists. Panel users are database records only.

---

## Phase 6 — Site Management

### 6.1 Site entity + repo  
✓ Insert site in SQLite  

### 6.2 WebServerManager (Nginx)
✓ Create Nginx vhost  
✓ Reload Nginx  

### 6.3 PhpRuntimeManager
✓ Detect FPM versions  
✓ Generate FPM pools  

### 6.4 Site creation pipeline
✓ Directory creation under `/opt/novapanel/sites/{panel_username}/{domain}/`
✓ Set ownership to `novapanel:novapanel`
✓ Vhost + FPM pool (both run as `novapanel` user)
✓ DNS zone creation  
✓ FTP optional (virtual user mapping to novapanel UID)
✓ DB optional  

**Checkpoint Validation:**
- Create site → accessible domain (test with dummy hosts entry)
- **Verify:** All files owned by `novapanel:novapanel`
- **Verify:** PHP-FPM pool config shows `user = novapanel`

---

## Phase 7 — DNS (PowerDNS)

### 7.1 PDNS MySQL Connection
✓ PDO connection pool  

### 7.2 PowerDnsManager
✓ createZone  
✓ addRecord  
✓ deleteRecord  

**Checkpoint Validation:**
- Zone appears in `domains` table.

---

## Phase 8 — Databases (MySQL/Postgres)

### 8.1 DB lifecycle
✓ Create DB  
✓ Create DB user  
✓ Grant privileges  

**Checkpoint Validation:**
- Login with DB credentials works.

---

## Phase 9 — Cron Manager

### 9.1 Adapter
✓ Write to `novapanel` user's crontab (single Linux user)
✓ Add, remove, list jobs  
✓ Tag jobs with panel user ownership in comments

**Critical:** All cron jobs run as `novapanel` user  

---

## Phase 10 — FTP (Pure-FTPd)

### 10.1 Adapter
✓ Create FTP virtual user via Pure-FTPd (pure-pw)
✓ Map all FTP users to `novapanel` UID/GID
✓ Set password  
✓ Enable/disable  

**Checkpoint Validation:**
- FTP login works
- **Verify:** FTP user is NOT a Linux system user
- **Verify:** FTP process runs as `novapanel`

**Critical:** NEVER use system-level FTP users - only Pure-FTPd virtual users

---

## Phase 11 — File Manager

### 11.1 File operations
✓ list  
✓ read  
✓ write  
✓ upload  
✓ delete  

---

## Phase 12 — UI & Theming

### 12.1 Template system
✓ Basic layout  
✓ Forms for accounts, sites, DNS, FTP  

---

## Phase 13 — Installer & Updater System

### 13.1 install.sh
✓ Install panel prerequisites  
✓ Run migrations  
✓ Create admin user  

---

## Phase 14 — Final Tests & Validation

### Automated checks:
- Site creation
- DNS zone creation
- FTP login
- DB creation
- Cron execution
- RBAC enforcement

---

## Phase 15 — Packaging & Release

### 15.1 Tarball / GitHub release  
### 15.2 Documentation
