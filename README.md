# NovaPanel

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

A lightweight, open-source single VPS control panel built with PHP. NovaPanel provides a simple yet powerful interface to manage websites, databases, DNS, FTP, and cron jobs on a single server.

## Features

- 🚀 **Site Management** - Create and manage multiple websites with ease
- 👥 **Panel User Management** - Create users with different roles and permissions
- 🐘 **PHP-FPM** - Multi-version PHP support with isolated pools
- 🌐 **Nginx** - High-performance web server configuration
- 📊 **Database Management** - MySQL/PostgreSQL database creation and management
- 🔐 **FTP Access** - Secure FTP user management
- ⏰ **Cron Jobs** - Schedule tasks for each panel user
- 🔒 **Role-Based Access Control** - Admin, Account Owner, Developer, Read-Only roles
- 🛡️ **Security First** - Non-root execution, command whitelisting, input validation
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
1. Install required dependencies
2. Create the panel user
3. Set up the database
4. Configure Nginx
5. Create an admin user
6. Set up security permissions

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

# Run database migration
sudo -u novapanel php database/migration.php

# Configure sudoers (see SECURITY.md for details)
sudo visudo -f /etc/sudoers.d/novapanel
```

## Usage

### Accessing the Panel

After installation, access the panel at:
```
http://your-server-ip:7080
```

The panel runs on port 7080 by default for security isolation from hosted sites.

### User Structure (Single VPS)

NovaPanel is designed for single VPS hosting with a simplified two-tier structure:

1. **Panel Users** - People who log into the NovaPanel interface
   - Created via **Panel Users** > **Create Panel User**
   - Have roles: Admin, Account Owner, Developer, Read-Only
   - Can own and manage multiple sites
   
2. **Sites** - Websites/domains owned by panel users
   - Created via **Sites** > **Create Site**
   - Each site belongs to one panel user
   - All sites run under the panel's system user (novapanel)
   - No separate Linux accounts - everything is managed through the panel

### Creating a Panel User

1. Log in as admin
2. Navigate to **Panel Users** > **Create Panel User**
3. Enter username, email, and password
4. Assign roles (Admin, Account Owner, Developer, Read-Only)
5. The panel user can now log in and manage their sites

### Creating a Website

1. Navigate to **Sites** > **Create Site**
2. Enter domain name (e.g., `example.com`)
3. Select an account
4. Choose PHP version
5. Enable SSL if needed
6. The system will:
   - Create document root
   - Generate Nginx vhost
   - Create PHP-FPM pool
   - Reload services

### Managing Databases

1. Navigate to **Databases** > **Create Database**
2. Enter database name
3. Select account
4. Choose database type (MySQL/PostgreSQL)
5. Create database user and assign privileges

### Setting Up DNS

1. Navigate to **DNS**
2. Select a domain
3. Add DNS records (A, AAAA, CNAME, TXT, MX, SRV)

### Managing Cron Jobs

1. Navigate to **Cron Jobs**
2. Select an account
3. Add cron schedule and command
4. Enable/disable jobs as needed

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