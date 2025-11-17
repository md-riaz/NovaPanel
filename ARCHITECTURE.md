# NovaPanel Architecture - Single VPS Model

## Overview

NovaPanel uses a **Single VPS Model** where all operations run under ONE Linux system account. This is fundamentally different from traditional hosting panels that create separate Linux users for each customer.

## Single Linux User Architecture

### The Core Principle

**IMPORTANT:** NovaPanel creates and manages ONLY ONE Linux system user: `novapanel`

- All websites run under the `novapanel` user
- All PHP-FPM pools run as `novapanel`
- All cron jobs run as `novapanel`
- All FTP users authenticate as `novapanel` (via Pure-FTPd virtual users)
- All files are owned by `novapanel`

### Panel Users vs. Linux Users

There are TWO types of users in NovaPanel:

1. **Linux System User (ONLY ONE)**
   - Username: `novapanel`
   - Created during installation
   - Owns all files and processes
   - Never deleted or modified

2. **Panel Users (MANY)**
   - Stored in SQLite database (`users` table)
   - Used for authentication to the panel interface
   - Used for organizing and tracking resource ownership
   - Have roles: Admin, AccountOwner, Developer, ReadOnly
   - **DO NOT correspond to Linux users**

## Directory Structure

All sites and resources are organized under the panel user's home directory:

```
/opt/novapanel/
├── sites/
│   ├── {panel_username_1}/
│   │   ├── example.com/
│   │   │   ├── public_html/
│   │   │   └── tmp/
│   │   └── another-site.com/
│   └── {panel_username_2}/
│       └── test.com/
├── storage/
│   ├── panel.db          # SQLite database
│   └── logs/
└── ... (panel files)
```

All files are owned by `novapanel:novapanel`.

## How Resources Work

### Websites (Sites)

- Each site has a document root like: `/opt/novapanel/sites/{panel_username}/{domain}/`
- Nginx serves files as the `novapanel` user
- PHP-FPM processes run as `user = novapanel` and `group = novapanel`
- No isolation between sites at the OS level
- Panel users can only access their own sites through the panel interface (enforced by application logic)

### PHP-FPM Pools

Each site gets its own PHP-FPM pool configuration:

```ini
[example_com]
user = novapanel
group = novapanel
listen = /var/run/php/php8.2-fpm-example.com.sock
```

**Key Point:** All pools run as the SAME Linux user (`novapanel`), but are separated by:
- Different socket files
- Different pool names
- Application-level access control

### FTP Access

FTP is managed via **Pure-FTPd virtual users**:

- Pure-FTPd's `pure-pw` creates virtual FTP users
- All virtual users map to the `novapanel` UID/GID
- FTP users are NOT Linux users
- Each FTP user can be restricted to a specific home directory
- Example: FTP user "john_ftp" → maps to UID of `novapanel` → home dir `/opt/novapanel/sites/john/example.com/`

### Databases

- MySQL/PostgreSQL databases are created normally
- Database users are standard MySQL/PostgreSQL users (not related to Linux users)
- Panel tracks which panel user owns which database
- No special Linux user mapping

### Cron Jobs

- All cron jobs are added to the `novapanel` user's crontab
- Cron jobs run as the `novapanel` user
- Panel adds a comment to identify which panel user owns each job:
  ```
  # NovaPanel user: john
  0 2 * * * /path/to/backup.sh
  ```

### DNS (BIND9)

- DNS is managed via BIND9 zone files stored in `/etc/bind/zones/`
- Complete isolation from database access (enhanced security)
- No Linux user involvement
- Panel tracks which panel user owns which domain
- Zone files are owned by `bind:bind` and managed through the panel

## Security Implications

### Advantages

1. **Simplicity**: No complex user management
2. **Performance**: No overhead from user switching
3. **Easy Management**: Single set of file permissions
4. **Resource Sharing**: Easier to share resources between sites

### Limitations

1. **No OS-Level Isolation**: Sites can potentially access each other's files
2. **Shared Resources**: All sites share the same resource limits
3. **Trust Required**: Panel users must be trusted not to abuse access

### Security Measures

To maintain security in the single-user model:

1. **Application-Level Access Control**
   - RBAC enforced in the panel
   - Panel users can only see/manage their own resources through the UI

2. **File Permissions**
   - All files are 755/644 (readable by owner)
   - Sensitive files should be outside document roots

3. **Process Isolation**
   - Each site has its own PHP-FPM pool
   - Pools are isolated from each other by PHP-FPM

4. **Database Access**
   - Each database has its own credentials
   - Credentials should be unique per database

5. **FTP Jails**
   - FTP users are jailed to specific directories
   - Cannot navigate outside their home directory

## Why Single VPS Model?

This architecture is suitable for:

- **Personal VPS**: Single person managing multiple sites
- **Small Agency**: Trusted team managing client sites
- **Development/Staging**: Non-production environments
- **Low-Budget Hosting**: Maximum resource utilization

This architecture is NOT suitable for:

- **Shared Hosting**: Untrusted customers
- **High-Security Environments**: Requiring strong isolation
- **Multi-Tenant SaaS**: Multiple untrusted organizations

## Implementation Notes

### Never Create Linux Users

The following commands should NEVER be used in NovaPanel:

- `useradd` - Don't create users
- `usermod` - Don't modify users
- `userdel` - Don't delete users
- `adduser` - Don't create users
- `groupadd` - Don't create groups

These commands have been removed from the `Shell` adapter's allowed commands list.

### Panel User Creation

When creating a panel user:

1. Insert into `users` table in SQLite
2. Hash password with `password_hash()`
3. Assign roles via `user_roles` table
4. Create directory `/opt/novapanel/sites/{username}/`
5. Set owner to `novapanel:novapanel`

**DO NOT** create a Linux user!

### Site Creation

When creating a site:

1. Insert into `sites` table (with `user_id` referencing panel user)
2. Create directory `/opt/novapanel/sites/{panel_username}/{domain}/`
3. Set owner to `novapanel:novapanel`
4. Create PHP-FPM pool with `user = novapanel`
5. Create Nginx vhost
6. Reload services

### FTP User Creation

When creating an FTP user:

1. Insert into `ftp_users` table (with `user_id` referencing panel user)
2. Run `pure-pw useradd` with the UID/GID of `novapanel`
3. Set home directory to site's document root
4. Pure-FTPd handles the virtual user mapping

### Cron Job Creation

When creating a cron job:

1. Insert into `cron_jobs` table (with `user_id` referencing panel user)
2. Add to `novapanel` user's crontab
3. Add comment identifying panel user
4. Job runs as `novapanel`

## Migration from Multi-User Systems

If migrating from a traditional multi-user panel:

1. All files must be re-owned to `novapanel:novapanel`
2. PHP-FPM pools must be updated to use `novapanel`
3. Cron jobs must be consolidated into `novapanel`'s crontab
4. FTP users must be recreated as Pure-FTPd virtual users

## Conclusion

NovaPanel's single VPS model provides simplicity and ease of management at the cost of OS-level isolation. This is a deliberate design choice suitable for trusted environments where strong isolation is not required.

**Remember:** ONE Linux user (`novapanel`), MANY panel users (database records).
