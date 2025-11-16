# AGENTS — Responsibility Map For AI Code Generation

This document defines each agent role and what it is allowed to do, validate, and coordinate.

---

## Agent 1 — Architect
**Primary Job:**  
Interpret design.md and produce correct folder structure, namespaces, interfaces, and high-level scaffolding.

**Capabilities:**
- Validate directory layout
- Ensure contracts & facades match design
- Prevent architecture drift

**Receives Input:** design.md  
**Produces Output:** boilerplate PHP classes, directory structure

**Checklist:**
- All interfaces exist under `/app/Contracts`
- Facades map correctly to manager factories
- Router, Controller structure validated

---

## Agent 2 — Database Engineer
**Primary Job:**  
Manage SQLite schema, migrations, repositories, and DB abstraction.

**Capabilities:**
- Generate DB schema SQL
- Write DB connection class
- Build repository classes
- Ensure DB consistency

**Receives Input:** design.md, implementation_plan.md  
**Produces:** `migration.php`, `Database.php`, repository layer

**Checklist:**
- All tables created successfully
- Repositories return Entities, not arrays
- No DB logic appears in controllers

---

## Agent 3 — Backend Implementer
**Primary Job:**  
Implement application services (CreateSiteService, CreateAccountService, etc.) and infrastructure adapters.

**Capabilities:**
- Write Nginx adapter
- Write PowerDNS adapter
- Write PHP-FPM adapter
- Write Cron adapter
- Write FTP adapter
- Implement Shell runner
- Implement 100% of use-cases

**Receives Input:** Contracts, Architecture  
**Produces:** Working backend logic

**Checklist:**
- Adapters follow interface
- Services call adapters properly
- OS interactions through Shell wrapper only

---

## Agent 4 — Security Guardian
**Primary Job:**  
Ensure:
- Non-root execution
- Sudo whitelist is minimal
- All shell commands are escaped
- No direct user input touches shell

**Receives Input:** Shell adapter code  
**Produces:** Clean, secure patches

**Checklist:**
- All `exec` calls reviewed
- Sudoers file validated
- Directory permissions valid

---

## Agent 5 — UI Builder
**Primary Job:**  
Build HTML/CSS/JS for views.

**Capabilities:**
- Create templates under `/resources/views`
- Build forms for site/account/dns/ftp/db
- Add dashboard layout
- Add navigation

**Checklist:**
- No backend logic in views
- use bootstrap and any cdn possible 
- Clean UX

---

## Agent 6 — Validator & Tester
**Primary Job:**  
Run through all checkpoints in implementation_plan.md and confirm functionality.

**Capabilities:**
- Generate tests
- Validate creation workflows
- Confirm RBAC enforcement
- Verify final system behavior with mock or real environment

**Checklist:**
- All major flows function
- No unimplemented contract
- No broken dependency chain

---

## Agent 7 — Packager & Documentor
**Primary Job:**  
Create release artifacts and documentation.

**Capabilities:**
- Build install.sh
- Write user guide
- Produce README
- Generate API docs (if any)

**Checklist:**
- Installer runs cleanly
- Admin user created
- Nginx panel vhost generated
