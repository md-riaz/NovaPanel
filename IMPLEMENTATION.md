# Implementation Plan (Checkpoints for AI Agents)

This plan is structured into stages and checkpoints so an AI agent can verify tasks before moving forward.

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
- users  
- roles  
- permissions  
- user_roles  
- accounts  
- sites  
- domains  
- ftp_users (optional shadow)  
- cron_jobs  
- databases  

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

**Note:** Accounts module removed - single VPS model links sites directly to panel users.

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
✓ Directory creation  
✓ Vhost + FPM pool  
✓ DNS zone creation  
✓ FTP optional  
✓ DB optional  

**Checkpoint Validation:**
- Create site → accessible domain (test with dummy hosts entry).

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
✓ Write crontab for system user  
✓ Add, remove, list jobs  

---

## Phase 10 — FTP (Pure-FTPd)

### 10.1 Adapter
✓ Create FTP user (system-level or MySQL backend)  
✓ Set password  
✓ Enable/disable  

**Checkpoint Validation:**
- FTP login works.

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
