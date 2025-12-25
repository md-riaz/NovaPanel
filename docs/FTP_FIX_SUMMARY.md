# FTP Connection Issue - Fix Summary

## Problem
Users reported that FTP credentials could be created through the NovaPanel interface, but FileZilla and other FTP clients could not establish connections to the server.

## Root Cause
Pure-FTPd was installed but not properly configured for modern FTP clients:

1. **Missing Passive Mode Configuration**: Pure-FTPd had no passive port range configured
2. **Firewall Restrictions**: Only port 21 (control) was open; data transfer ports were blocked
3. **Incomplete Security Settings**: Missing chroot, anonymous disable, and umask configurations

## Technical Background

### How FTP Works
FTP uses two separate connections:
- **Control Connection** (Port 21): Commands and responses
- **Data Connection** (Variable ports): Actual file transfers

### Active vs Passive Mode
- **Active Mode**: Server initiates data connection to client (problematic with firewalls/NAT)
- **Passive Mode**: Client initiates both connections (works better with modern networks)

Modern FTP clients like FileZilla use **passive mode by default**, which requires:
- Server to have a range of ports open for data connections
- Firewall to allow incoming connections on that port range

## Solution Implemented

### 1. Pure-FTPd Configuration
Added to `/etc/pure-ftpd/conf/`:

```
PassivePortRange: 30000 30100
ChrootEveryone: yes
NoAnonymous: yes
Umask: 022:022
PureDB: /etc/pure-ftpd/pureftpd.pdb
```

### 2. Firewall Configuration
Updated UFW rules:
```bash
ufw allow 21/tcp              # FTP control connection
ufw allow 30000:30100/tcp     # FTP passive mode data connections
```

### 3. Documentation
- Created `docs/FTP_SETUP.md` with comprehensive setup guide
- Added troubleshooting section for common issues
- Documented FileZilla, WinSCP, and CLI connection methods

### 4. Verification Script
Created `scripts/verify-ftp.sh` to automatically check:
- Pure-FTPd installation and service status
- Configuration file presence and content
- Firewall rules
- FTP user database
- Port listening status

## Files Changed

1. **install.sh**
   - Lines 183-218: Pure-FTPd configuration with passive mode
   - Lines 549-551: Firewall rules for passive ports
   - Lines 590-608: Updated installation messages

2. **docs/FTP_SETUP.md** (NEW)
   - Complete FTP setup guide
   - Client configuration examples
   - Troubleshooting section
   - Security best practices

3. **scripts/verify-ftp.sh** (NEW)
   - Automated configuration verification
   - Comprehensive system checks

4. **README.md**
   - Updated FTP feature description
   - Added documentation link

## Testing Instructions

### For New Installations
Run the updated installer:
```bash
sudo bash install.sh
```

### For Existing Installations
Re-run the Pure-FTPd configuration section:

```bash
# Create conf directory
sudo mkdir -p /etc/pure-ftpd/conf

# Configure passive mode
echo "30000 30100" | sudo tee /etc/pure-ftpd/conf/PassivePortRange
echo "yes" | sudo tee /etc/pure-ftpd/conf/ChrootEveryone
echo "yes" | sudo tee /etc/pure-ftpd/conf/NoAnonymous
echo "022:022" | sudo tee /etc/pure-ftpd/conf/Umask

# Open firewall ports
sudo ufw allow 30000:30100/tcp

# Restart Pure-FTPd
sudo systemctl restart pure-ftpd

# Verify configuration
sudo bash /opt/novapanel/scripts/verify-ftp.sh
```

### Testing FTP Connection

1. **Create FTP User in Panel**
   - Go to NovaPanel → FTP Users → Create
   - Username: `testuser`
   - Password: Strong password (min 8 chars)
   - Home Directory: `/opt/novapanel/sites/test/`

2. **Connect with FileZilla**
   - Host: Your server IP
   - Port: 21
   - Protocol: FTP
   - Username: `testuser`
   - Password: Your password
   - Transfer Settings: Passive (default)

3. **Expected Result**
   - Connection successful
   - Can browse home directory
   - Can upload/download files
   - User is jailed to home directory

## Cloud Provider Notes

### AWS EC2
Ensure Security Group allows:
- Port 21/tcp (inbound)
- Ports 30000-30100/tcp (inbound)

### DigitalOcean
Ensure Firewall allows:
- Port 21/tcp (inbound)
- Ports 30000-30100/tcp (inbound)

### Other Cloud Providers
Check your provider's firewall/security group settings and allow the same ports.

## Security Considerations

### What We Implemented
1. **Virtual Users**: All FTP users map to `novapanel` UID (no Linux user creation)
2. **Chroot Jail**: Users cannot access files outside their home directory
3. **No Anonymous**: Anonymous FTP disabled
4. **Proper Umask**: Files created with rw-r--r-- (644), directories with rwxr-xr-x (755)

### Recommendations
1. Use strong passwords (12+ characters)
2. Limit FTP home directories to minimum required paths
3. Consider enabling FTPS (FTP over TLS) for encrypted connections
4. Regular audits of FTP users
5. Monitor logs for suspicious activity

## Troubleshooting

### Connection Timeout
- Check firewall: `sudo ufw status`
- Check Pure-FTPd: `sudo systemctl status pure-ftpd`
- Check cloud security groups

### Cannot List Directory
- Passive mode ports likely blocked
- Run verification script: `sudo bash /opt/novapanel/scripts/verify-ftp.sh`

### Permission Denied
- Check directory ownership: `ls -la /opt/novapanel/sites/`
- Should be owned by `novapanel:novapanel`
- Fix with: `sudo chown -R novapanel:novapanel /opt/novapanel/sites/your-dir/`

## Impact

### Who Benefits
- All NovaPanel users who need FTP access
- Users migrating from cPanel/Plesk who expect FTP to work
- Users who use FileZilla or other modern FTP clients

### Backward Compatibility
- No breaking changes
- Existing FTP users continue to work
- Only affects new installations and users who apply the fix

## References

- Issue: FTP connection failure with FileZilla
- Pure-FTPd Documentation: https://www.pureftpd.org/
- FileZilla Documentation: https://wiki.filezilla-project.org/
- NovaPanel FTP Guide: docs/FTP_SETUP.md
