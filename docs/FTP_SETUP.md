# FTP Setup and Connection Guide

## Overview

NovaPanel uses **Pure-FTPd** with virtual users for secure FTP access. All FTP users are mapped to the `novapanel` system user for security and simplicity.

## Server Configuration

### Passive Mode
Pure-FTPd is configured to use **passive mode** (PASV) for data connections:
- **Control Port**: 21 (TCP)
- **Passive Port Range**: 30000-30100 (TCP)

Passive mode is required for most modern FTP clients (like FileZilla) and works better with firewalls and NAT.

### Firewall Requirements
The installation script automatically configures the firewall to allow:
- Port 21/tcp (FTP control connection)
- Ports 30000:30100/tcp (FTP passive mode data connections)

If you're using a different firewall or cloud provider security groups, ensure these ports are open.

## Creating FTP Users

### Through the Panel Interface

1. Log in to NovaPanel (http://your-server-ip:7080)
2. Navigate to **FTP Users** in the sidebar
3. Click **Create FTP User**
4. Fill in the form:
   - **FTP Username**: 3-32 alphanumeric characters (underscores and hyphens allowed)
   - **Panel User (Owner)**: Select the panel user who owns this FTP account
   - **Password**: Minimum 8 characters (use a strong password)
   - **Home Directory**: Must be within `/opt/novapanel/sites/`
5. Click **Create FTP User**

### Example Home Directories
- `/opt/novapanel/sites/example.com/public_html` - For a specific website
- `/opt/novapanel/sites/username/` - For a user's entire site directory
- `/opt/novapanel/sites/username/logs/` - For access to specific subdirectories

**Important**: Users are automatically jailed (chrooted) to their home directory for security.

## Connecting with FTP Clients

### FileZilla Configuration

1. Open FileZilla
2. Go to **File** → **Site Manager**
3. Click **New Site**
4. Configure the connection:
   - **Protocol**: FTP - File Transfer Protocol
   - **Host**: Your server's IP address or hostname
   - **Port**: 21
   - **Encryption**: Use explicit FTP over TLS if available (recommended)
   - **Logon Type**: Normal
   - **User**: Your FTP username (created in the panel)
   - **Password**: Your FTP password

5. Click **Transfer Settings** tab:
   - **Transfer mode**: Passive (recommended)

6. Click **Connect**

### WinSCP Configuration

1. Open WinSCP
2. Click **New Session**
3. Configure:
   - **File protocol**: FTP
   - **Host name**: Your server IP
   - **Port number**: 21
   - **User name**: Your FTP username
   - **Password**: Your FTP password

4. Click **Advanced** → **Connection** → **FTP**:
   - **Passive mode**: Check this option

5. Click **Save** and then **Login**

### Command Line FTP

```bash
ftp your-server-ip
# Enter username when prompted
# Enter password when prompted
```

For passive mode from command line:
```bash
ftp -p your-server-ip
```

## Troubleshooting

### Cannot Connect / Connection Timeout

**Check 1: Firewall Configuration**
Ensure ports are open on your server:
```bash
sudo ufw status
```

You should see:
```
21/tcp                     ALLOW       Anywhere
30000:30100/tcp           ALLOW       Anywhere
```

**Check 2: Cloud Provider Security Groups**
If you're using AWS, DigitalOcean, or similar, ensure your security group allows:
- Port 21/tcp (inbound)
- Ports 30000-30100/tcp (inbound)

**Check 3: Pure-FTPd Service Status**
```bash
sudo systemctl status pure-ftpd
```

If not running:
```bash
sudo systemctl start pure-ftpd
```

### Connection Established but Cannot List Directory

This usually means passive mode ports are blocked. Ensure ports 30000-30100 are open in your firewall.

### Permission Denied Errors

All FTP users run as the `novapanel` system user. Ensure the home directory and its contents have proper permissions:
```bash
sudo chown -R novapanel:novapanel /opt/novapanel/sites/your-directory/
sudo chmod -R 755 /opt/novapanel/sites/your-directory/
```

### Verify FTP User Exists

Check if your FTP user is in the Pure-FTPd database:
```bash
sudo pure-pw list
```

### Check Pure-FTPd Configuration

View current configuration:
```bash
sudo ls -la /etc/pure-ftpd/conf/
sudo cat /etc/pure-ftpd/conf/PassivePortRange
sudo cat /etc/pure-ftpd/conf/PureDB
```

Expected output:
- `PassivePortRange`: `30000 30100`
- `PureDB`: `/etc/pure-ftpd/pureftpd.pdb`

## Security Best Practices

1. **Use Strong Passwords**: Always use passwords with 12+ characters including uppercase, lowercase, numbers, and symbols
2. **Limit Access**: Set FTP home directories to the minimum required path
3. **Use FTPS**: Consider using explicit FTP over TLS for encrypted connections
4. **Regular Audits**: Review FTP users periodically and remove unused accounts
5. **Monitor Logs**: Check `/var/log/syslog` for FTP-related security events

## Advanced Configuration

### Changing Passive Port Range

If you need to change the passive port range:

1. Edit the configuration:
```bash
sudo sh -c 'echo "40000 40100" > /etc/pure-ftpd/conf/PassivePortRange'
```

2. Update firewall:
```bash
sudo ufw delete allow 30000:30100/tcp
sudo ufw allow 40000:40100/tcp
```

3. Restart Pure-FTPd:
```bash
sudo systemctl restart pure-ftpd
```

### Enable TLS/SSL for FTP

For secure FTP connections:

1. Generate a certificate:
```bash
sudo mkdir -p /etc/ssl/private/
sudo openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout /etc/ssl/private/pure-ftpd.pem \
  -out /etc/ssl/private/pure-ftpd.pem
sudo chmod 600 /etc/ssl/private/pure-ftpd.pem
```

2. Enable TLS in Pure-FTPd:
```bash
sudo sh -c 'echo "1" > /etc/pure-ftpd/conf/TLS'
sudo systemctl restart pure-ftpd
```

3. In FileZilla, use **FTP over explicit TLS** protocol

## Support

For issues or questions:
- GitHub Issues: https://github.com/md-riaz/NovaPanel/issues
- Documentation: https://github.com/md-riaz/NovaPanel

## Technical Details

- **FTP Server**: Pure-FTPd
- **Authentication**: PureDB (virtual users)
- **User Mapping**: All FTP users → `novapanel` UID/GID
- **Chroot**: Enabled (users jailed to home directory)
- **Anonymous FTP**: Disabled
- **Default Umask**: 022:022 (files: rw-r--r--, directories: rwxr-xr-x)
