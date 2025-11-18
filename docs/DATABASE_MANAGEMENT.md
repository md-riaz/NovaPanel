# Database Management in NovaPanel

## Overview

NovaPanel includes integrated database management capabilities through **Adminer**, a lightweight, single-file database management tool (similar to phpMyAdmin).

## Features

The integrated database manager provides:

- **Browse and Search**: View table structures and data
- **SQL Editor**: Execute custom SQL queries
- **Import/Export**: Backup and restore databases
- **User Management**: Manage database users and privileges  
- **Table Operations**: Create, modify, and drop tables
- **Data Editing**: Insert, update, and delete records directly
- **Multi-Database Support**: MySQL/MariaDB and PostgreSQL (when configured)

## Accessing the Database Manager

### Method 1: General Access

1. Navigate to **Databases** in the NovaPanel sidebar
2. Click the **Database Manager** button at the top-right
3. The database management interface opens in a new tab
4. You're automatically logged in with panel credentials

### Method 2: Direct Database Access

1. Navigate to **Databases** in the NovaPanel sidebar
2. Find the database you want to access in the list
3. Click the **Access** button next to that database
4. The database management interface opens with that database pre-selected

## Security

### Authentication

- **Automatic Login**: The database manager uses NovaPanel's authentication system
- **No Credential Entry**: Users don't need to enter MySQL credentials manually
- **Session-Based**: Access is tied to your NovaPanel session
- **Secure**: Database credentials are never exposed to the user

### Access Control

- Only authenticated NovaPanel users can access the database manager
- Users have access to all databases created through the panel
- Access is controlled through NovaPanel's MySQL root credentials configured during installation

## Configuration

The database manager uses credentials from `.env.php`:

```php
putenv('MYSQL_HOST=localhost');
putenv('MYSQL_ROOT_USER=novapanel_db');
putenv('MYSQL_ROOT_PASSWORD=your_password');
```

These credentials are automatically configured during installation.

## Common Tasks

### Creating a New Table

1. Access the database manager
2. Select your database from the left sidebar
3. Click "Create table"
4. Define table structure and fields
5. Click "Save"

### Importing Data

1. Access the database manager
2. Select your database
3. Click "Import" from the top menu
4. Choose your SQL file or paste SQL commands
5. Click "Execute"

### Exporting a Database

1. Access the database manager
2. Select your database
3. Click "Export" from the top menu
4. Choose format (SQL, CSV, etc.)
5. Click "Export" to download

### Running SQL Queries

1. Access the database manager
2. Select your database
3. Click "SQL command" from the top menu
4. Enter your SQL query
5. Click "Execute"

## Comparison: Adminer vs phpMyAdmin

NovaPanel uses **Adminer** instead of phpMyAdmin because:

| Feature | Adminer | phpMyAdmin |
|---------|---------|------------|
| Size | ~500KB (single file) | ~11MB (many files) |
| Installation | Drop-in, no setup | Complex setup required |
| Performance | Faster, lightweight | Slower, resource-heavy |
| Security | Simpler attack surface | More complex, more vulnerabilities |
| Updates | Single file replacement | Multi-step upgrade process |
| UI/UX | Clean, modern | Traditional, feature-rich |

## Troubleshooting

### Can't Access Database Manager

**Issue**: Database Manager button not working

**Solutions**:
1. Ensure you're logged into NovaPanel
2. Check that MySQL credentials are configured in `.env.php`
3. Verify MySQL service is running: `sudo systemctl status mysql`

### Connection Refused Error

**Issue**: "Cannot connect to database server"

**Solutions**:
1. Verify MySQL is running: `sudo systemctl restart mysql`
2. Check MySQL credentials in `.env.php`
3. Ensure MySQL user has proper permissions
4. Check MySQL error logs: `/var/log/mysql/error.log`

### Blank Page or Error

**Issue**: Database manager shows blank page or PHP errors

**Solutions**:
1. Check PHP error logs: `/var/log/php-fpm/error.log`
2. Verify Adminer file exists: `/opt/novapanel/public/adminer.php`
3. Check file permissions: `chmod 644 /opt/novapanel/public/adminer.php`
4. Restart PHP-FPM: `sudo systemctl restart php8.2-fpm`

## Advanced Usage

### Customizing Adminer Theme

The Adminer interface uses custom CSS to match NovaPanel's theme. To customize:

1. Edit `/opt/novapanel/public/assets/css/adminer-custom.css`
2. Modify colors, fonts, or layout as needed
3. Refresh the database manager to see changes

### Using Adminer Plugins

Adminer supports plugins for extended functionality. To add plugins:

1. Download desired plugin from [Adminer Plugins](https://www.adminer.org/plugins/)
2. Create `plugins` directory in `/opt/novapanel/public/`
3. Add plugin files to the directory
4. Modify `db-access.php` to load plugins

See the [Adminer Plugins Documentation](https://www.adminer.org/plugins/) for more details.

## Security Best Practices

1. **Keep NovaPanel Updated**: Regular updates include Adminer security patches
2. **Use Strong Passwords**: Ensure MySQL credentials are strong and secure
3. **Limit Access**: Only grant database access to trusted panel users
4. **Regular Backups**: Export databases regularly using the Export feature
5. **Monitor Access**: Check NovaPanel audit logs for database access patterns
6. **Secure Connection**: Use HTTPS for the panel to encrypt all traffic

## Additional Resources

- [Adminer Official Documentation](https://www.adminer.org/)
- [NovaPanel Security Documentation](../SECURITY.md)
- [MySQL Documentation](https://dev.mysql.com/doc/)
- [MariaDB Documentation](https://mariadb.com/kb/en/documentation/)

## Support

For issues or questions about database management:

- Check the [NovaPanel Issue Tracker](https://github.com/md-riaz/NovaPanel/issues)
- Read the [FAQ](https://github.com/md-riaz/NovaPanel/wiki)
- Join the [Discussion Forum](https://github.com/md-riaz/NovaPanel/discussions)
