# NovaPanel

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

A lightweight, open-source single VPS control panel built with PHP. NovaPanel provides a simple yet powerful interface to manage websites, databases, DNS, FTP, and cron jobs on a single server.

## Features

- 🚀 **Site Management** - Create and manage multiple websites with ease
- 👥 **Panel User Management** - Create users with different roles and permissions
- 🔐 **Authentication & Security** - Session-based authentication with CSRF protection and rate limiting
- 🐘 **PHP-FPM** - Multi-version PHP support with isolated pools
- 🌐 **Nginx** - High-performance web server configuration
- 📊 **Database Management** - MySQL/PostgreSQL database creation and management
- 🔐 **FTP Access** - Secure FTP user management
- ⏰ **Cron Jobs** - Schedule tasks for each panel user
- 💻 **Web Terminal** - Browser-based terminal access using ttyd (similar to cPanel)
- 🔒 **Role-Based Access Control** - Admin, Account Owner, Developer, Read-Only roles
- 📝 **Audit Logging** - Comprehensive logging of all admin actions and resource changes
- 🛡️ **Security First** - Non-root execution, command whitelisting, input validation, rate limiting
- 🖥️ **Single VPS Design** - All sites run under the panel user, no separate system accounts needed

## Requirements

- Ubuntu 20.04+ or Debian 11+
- PHP 8.2 or higher
- Nginx
- MySQL/MariaDB or PostgreSQL
- SQLite3
- Composer

## Quick Start

### Installation

```bash
# Clone the repository
git clone https://github.com/md-riaz/NovaPanel.git
cd NovaPanel

# Run the installer (requires root)
sudo bash install.sh
```

The installer will:
1. Install required dependencies (Nginx, PHP, MySQL, etc.)
2. Create the panel user
3. Set up the database
4. Create MySQL user for panel database management (auto-generated password)
5. Optionally install and configure PowerDNS for DNS management
6. Configure Nginx
7. Create an admin user
8. Set up security permissions

### Manual Installation

If you prefer manual installation:

```bash
# Install dependencies
sudo apt update
sudo apt install -y nginx php8.2-fpm php8.2-cli php8.2-sqlite3 \
    php8.2-mysql composer sqlite3

# Create panel user
sudo useradd -r -m -d /opt/novapanel -s /bin/bash novapanel

# Copy files
sudo cp -r . /opt/novapanel/
cd /opt/novapanel

# Install PHP dependencies
sudo -u novapanel composer install

# Create configuration file
sudo -u novapanel cp .env.php.example .env.php
sudo -u novapanel nano .env.php  # Edit and add your credentials
sudo chmod 600 .env.php

# Run database migration
sudo -u novapanel php database/migration.php

# Create admin user
sudo -u novapanel sqlite3 storage/panel.db
# Then run SQL:
# INSERT INTO users (username, email, password) VALUES ('admin', 'admin@example.com', '<hashed_password>');
# INSERT INTO user_roles (user_id, role_id) SELECT id, (SELECT id FROM roles WHERE name='Admin') FROM users WHERE username='admin';

# Configure sudoers (see SECURITY.md for details)
sudo visudo -f /etc/sudoers.d/novapanel
```

## Configuration

NovaPanel uses environment variables for sensitive configuration. The configuration file is located at `.env.php` in the panel root directory.

### Database Architecture

**Important:** NovaPanel uses **SQLite** for all panel operations (users, sites, permissions, etc.). The panel database is stored at `/opt/novapanel/storage/panel.db`.

MySQL and PostgreSQL credentials in the configuration are **ONLY** used when creating databases for panel users' websites - the panel itself does not use MySQL or PostgreSQL for its own operations.

### Automated Configuration (Recommended)

When you run `install.sh`, the configuration file is **automatically generated** with:
- **MySQL user** (`novapanel_db`) created with a secure random password
- **PowerDNS** (optional) - if installed, user and database are auto-configured
- **PostgreSQL** - left empty (install separately if needed)

No manual password entry required for MySQL or PowerDNS!

### Manual Configuration (Advanced)

If you need to manually configure after installation:

```bash
# Edit the auto-generated configuration
nano /opt/novapanel/.env.php

# Ensure secure permissions
chmod 600 /opt/novapanel/.env.php
chown novapanel:novapanel /opt/novapanel/.env.php
```

### Configuration Options

- **Panel Database**: SQLite (automatically configured at `storage/panel.db`)
- **MySQL Credentials**: Auto-generated user (`novapanel_db`) for creating CUSTOMER databases (not for panel operations)
- **PostgreSQL Credentials**: Empty by default (install and configure separately if needed)
- **PowerDNS Credentials**: Auto-generated if you chose to install PowerDNS during setup
- **Application Settings**: Environment, debug mode, and panel URL

See `.env.php.example` for the configuration file structure.

## Usage

### Accessing the Panel

After installation, access the panel at:
```
http://your-server-ip:7080
```

**Default Login:** Use the admin credentials you created during installation.

The panel runs on port 7080 by default for security isolation from hosted sites.

**Security Note:** The panel initially runs over HTTP. For production use, it's strongly recommended to:
1. Set up an SSL certificate (e.g., using Let's Encrypt)
2. Configure Nginx to serve the panel over HTTPS
3. Once HTTPS is enabled, session cookies will automatically use the secure flag

### User Structure (Single VPS - Single System Account)

NovaPanel uses a **Single VPS Model** where **everything runs under ONE Linux system account** (`novapanel`):

#### Understanding the Two Types of "Users"

1. **Linux System User (ONLY ONE)**
   - Username: `novapanel`
   - Created during installation
   - Owns ALL files and runs ALL processes
   - Never create additional Linux users!
   
2. **Panel Users (MANY)**
   - Database records in SQLite (`users` table)
   - Used for logging into the panel web interface
   - Have roles: Admin, Account Owner, Developer, Read-Only
   - Own and manage sites, databases, FTP accounts
   - **NOT Linux users** - just database records

#### How It Works

- **All websites** run as the `novapanel` Linux user
- **All PHP-FPM pools** run as `novapanel`
- **All cron jobs** run as `novapanel`
- **All FTP users** map to the `novapanel` UID (via Pure-FTPd virtual users)
- **All files** are owned by `novapanel:novapanel`

Panel users are only for organizing and controlling access through the web interface. The actual Linux system only knows about one user: `novapanel`.

#### Directory Structure

```
/opt/novapanel/sites/
├── john/                    # Panel user "john"
│   ├── example.com/         # John's first site
│   └── shop.com/            # John's second site
└── mary/                    # Panel user "mary"
    └── blog.com/            # Mary's site
```

All directories owned by `novapanel:novapanel`.

**CRITICAL:** There is only ONE Linux system user. Panel users exist only in the database for access control. See [ARCHITECTURE.md](ARCHITECTURE.md) for full details.

### Creating a Panel User

1. Log in as admin
2. Navigate to **Panel Users** > **Create Panel User**
3. Enter username, email, and password
4. Assign roles (Admin, Account Owner, Developer, Read-Only)
5. The panel user can now log in and manage their sites

### Creating a Website

1. Navigate to **Sites** > **Create Site**
2. Enter domain name (e.g., `example.com`)
3. Select the panel user who will own this site
4. Choose PHP version
5. Enable SSL if needed
6. The system will:
   - Create document root under `/opt/novapanel/sites/{username}/{domain}`
   - Generate Nginx vhost
   - Create PHP-FPM pool
   - Reload services

### Managing Databases

1. Navigate to **Databases** > **Create Database**
2. Enter database name
3. Select panel user who will own the database
4. Choose database type (MySQL/PostgreSQL)
5. Create database user and assign privileges

### Setting Up DNS

1. Navigate to **DNS**
2. Select a domain
3. Add DNS records (A, AAAA, CNAME, TXT, MX, SRV)

### Managing Cron Jobs

1. Navigate to **Cron Jobs**
2. Select a panel user
3. Add cron schedule and command
4. Enable/disable jobs as needed

### Using the Terminal

NovaPanel includes a web-based terminal feature powered by **ttyd**, similar to cPanel's terminal:

1. Navigate to **Terminal** from the sidebar
2. If ttyd is not installed, follow the on-screen installation instructions:
   ```bash
   # Ubuntu/Debian
   sudo apt update
   sudo apt install ttyd
   ```
3. Once installed, the terminal will open automatically
4. All commands run as the `novapanel` system user
5. You have limited sudo access for approved panel operations
6. Sessions automatically timeout after inactivity for security

**Terminal Features:**
- Full-featured terminal with Xterm.js
- Clipboard support (copy/paste)
- Command history and auto-completion
- Secure credential-based authentication
- Session management (start, stop, restart)
- Real-time command execution

**Common Tasks:**
- Check site files: `ls -la /opt/novapanel/sites/`
- View logs: `tail -f /opt/novapanel/storage/logs/shell.log`
- Test Nginx config: `sudo nginx -t`
- Manage services: `sudo systemctl status nginx`

## Architecture

NovaPanel follows a clean architecture pattern:

```
app/
├── Contracts/          # Interface definitions
├── Domain/            # Domain entities
├── Facades/           # Service facades
├── Http/              # HTTP layer (Router, Controllers)
├── Infrastructure/    # Adapters and implementations
├── Repositories/      # Data access layer
└── Services/          # Application services
```

See [DESIGN.md](DESIGN.md) for detailed architecture documentation.

## Development

### Running Tests

```bash
composer test
```

### Development Server

```bash
cd public
php -S localhost:8000
```

### Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

See [AGENTS.md](AGENTS.md) for the AI agent responsibility map.

## Security

NovaPanel takes security seriously:

- ✅ Non-root execution model
- ✅ Command whitelisting
- ✅ Sudo restrictions
- ✅ Input validation
- ✅ Shell argument escaping
- ✅ Prepared SQL statements
- ✅ Password hashing

See [SECURITY.md](SECURITY.md) for detailed security documentation.

## Documentation

- [DESIGN.md](DESIGN.md) - System design and architecture
- [IMPLEMENTATION.md](IMPLEMENTATION.md) - Implementation plan
- [AGENTS.md](AGENTS.md) - AI agent responsibility map
- [SECURITY.md](SECURITY.md) - Security considerations

## Roadmap

- [x] Web-based terminal (ttyd integration)
- [ ] Email server integration
- [ ] Automated backups
- [ ] REST API
- [ ] Plugin system
- [ ] Multi-server support
- [ ] Let's Encrypt integration
- [ ] File manager
- [ ] Resource monitoring
- [ ] Two-factor authentication

## License

NovaPanel is open-source software licensed under the [MIT license](LICENSE).

## Support

- 📖 [Documentation](https://github.com/md-riaz/NovaPanel/wiki)
- 🐛 [Issue Tracker](https://github.com/md-riaz/NovaPanel/issues)
- 💬 [Discussions](https://github.com/md-riaz/NovaPanel/discussions)

## Credits

Created and maintained by [md-riaz](https://github.com/md-riaz)

---

**Note:** This is a single-server control panel designed for VPS hosting. It is not designed for shared hosting or multi-server environments.