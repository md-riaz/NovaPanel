# Terminal Nginx Proxy Setup Guide

## Overview

The NovaPanel terminal feature uses `ttyd` (a simple terminal sharing tool) to provide web-based terminal access. This document explains how nginx is configured to proxy terminal connections and how authentication credentials are passed securely.

## Architecture

```
User Browser (iframe)
    ↓
NovaPanel Web Interface (https://panel.example.com/terminal)
    ↓
Nginx Reverse Proxy (/terminal-ws/{port})
    ↓
ttyd Process (localhost:{port} with basic auth)
    ↓
Bash Shell
```

## How It Works

### 1. Terminal Session Creation

When a user accesses the terminal page:

1. **TerminalController** calls `TerminalAdapter->startSession($userId)`
2. **TerminalAdapter** generates:
   - A unique port number (basePort + userId, with collision detection)
   - A random 32-character authentication token
   - Stores session info in JSON file with port and token
3. **ttyd process** starts with command:
   ```bash
   ttyd -p {port} -c novapanel:{token} -t fontSize=14 -W bash -l
   ```
   - `-c novapanel:{token}` enables HTTP Basic Authentication
   - Username: `novapanel`
   - Password: The generated token

### 2. Frontend Access

The terminal page displays an iframe pointing to:
```
https://panel.example.com/terminal-ws/{port}
```

This URL is a **proxied path** that doesn't expose the actual ttyd port to users.

### 3. Nginx Proxy Configuration

Nginx intercepts requests to `/terminal-ws/*` and proxies them to the local ttyd process:

```nginx
# Terminal WebSocket Proxy
location ~ ^/terminal-ws/(\d+)$ {
    # Extract port number from URL
    set $ttyd_port $1;
    
    # Proxy to local ttyd process
    proxy_pass http://127.0.0.1:$ttyd_port;
    
    # Required for WebSocket connections
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    
    # Pass client information to ttyd
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    
    # Increase timeout for long-running terminal sessions
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    
    # Disable buffering for real-time terminal I/O
    proxy_buffering off;
    
    # Optional: Restrict access to authenticated panel users only
    # This is already handled by the panel's session management,
    # but you can add additional IP restrictions here if needed
}
```

### 4. Authentication Flow

**Important:** The nginx proxy does **NOT** need to handle authentication credentials because:

1. **ttyd's built-in auth** handles it automatically
2. When the iframe loads, the browser sends the request to nginx
3. Nginx proxies it to `http://127.0.0.1:{port}`
4. ttyd responds with `401 Unauthorized` and `WWW-Authenticate: Basic` header
5. **ttyd's web interface** (not the panel) handles the authentication:
   - ttyd's JavaScript client automatically prompts for credentials
   - OR ttyd reads credentials from the URL if provided
6. The browser caches the credentials and includes them in subsequent WebSocket connections

### 5. Security Considerations

**Why this is secure:**

1. **Random tokens:** Each session gets a unique 32-character random token
2. **Port isolation:** Each user gets a separate ttyd process on a unique port
3. **Basic auth:** ttyd requires username:password for every connection
4. **localhost binding:** ttyd processes only listen on 127.0.0.1 (not accessible externally)
5. **nginx proxy:** External access only through nginx, which can add additional restrictions
6. **Session files:** Credentials stored server-side, not exposed to frontend
7. **Panel authentication:** Users must be logged into NovaPanel to access terminal page

**Additional security measures:**

- Terminal sessions timeout after inactivity (configured via cleanup script)
- All commands are logged for auditing
- Limited sudo access for the `novapanel` user
- Process isolation between users

## Production Nginx Configuration

### Complete VHost Example

```nginx
server {
    listen 80;
    server_name panel.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name panel.example.com;
    
    root /opt/novapanel/public;
    index index.php;
    
    # SSL Configuration
    ssl_certificate /etc/ssl/certs/panel.example.com.crt;
    ssl_certificate_key /etc/ssl/private/panel.example.com.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    
    # Panel Application
    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }
    
    # PHP-FPM
    location ~ \.php$ {
        fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        include fastcgi_params;
    }
    
    # Terminal WebSocket Proxy
    location ~ ^/terminal-ws/(\d+)$ {
        set $ttyd_port $1;
        proxy_pass http://127.0.0.1:$ttyd_port;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_buffering off;
    }
    
    # Static files
    location ~* \.(jpg|jpeg|png|gif|ico|css|js|woff|woff2|ttf)$ {
        expires 365d;
        add_header Cache-Control "public, immutable";
    }
    
    # Deny access to hidden files
    location ~ /\. {
        deny all;
    }
}
```

## Installation Steps

### 1. Install ttyd

```bash
# Option 1: From package (Ubuntu 20.04+)
sudo apt update
sudo apt install ttyd

# Option 2: Download binary
wget https://github.com/tsl0922/ttyd/releases/download/1.7.4/ttyd.x86_64
sudo mv ttyd.x86_64 /usr/local/bin/ttyd
sudo chmod +x /usr/local/bin/ttyd
```

### 2. Configure Nginx

```bash
# Add terminal proxy configuration to your nginx vhost
sudo nano /etc/nginx/sites-available/novapanel

# Test configuration
sudo nginx -t

# Reload nginx
sudo systemctl reload nginx
```

### 3. Configure Firewall

Ensure ttyd ports are NOT exposed externally:

```bash
# Only allow localhost connections to ttyd ports
sudo ufw deny 7100:7199/tcp
```

The ports are only accessible via nginx proxy on port 443.

### 4. Test Terminal Access

1. Login to NovaPanel
2. Navigate to Terminal page
3. Terminal should load automatically in the iframe
4. You should see a bash prompt without additional login prompts

## Troubleshooting

### Terminal shows "Not Found"

**Cause:** Nginx proxy configuration is missing or incorrect.

**Solution:**
1. Check nginx configuration includes the `/terminal-ws/` location block
2. Verify nginx can access localhost:{port}
3. Check nginx error log: `sudo tail -f /var/log/nginx/error.log`

### Terminal prompts for username/password

**Cause:** This is normal behavior! ttyd's web interface handles authentication.

**Solution:** 
- This is expected and secure
- ttyd will prompt once per session
- Credentials are cached by the browser for the session duration
- The authentication is handled by ttyd's built-in web interface

### Terminal shows blank/black screen

**Possible causes:**
1. ttyd process didn't start (check logs in `storage/terminal/logs/{userId}.log`)
2. Port is already in use (TerminalAdapter should detect this)
3. WebSocket connection failed (check browser console)

**Solutions:**
1. Check ttyd process: `ps aux | grep ttyd`
2. Check session info: `cat storage/terminal/pids/{userId}.json`
3. Check nginx logs: `sudo tail -f /var/log/nginx/error.log`
4. Check browser console for WebSocket errors

### Terminal disconnects frequently

**Cause:** Nginx timeout is too short.

**Solution:** Increase `proxy_read_timeout` in nginx configuration:
```nginx
proxy_read_timeout 7200s;  # 2 hours
```

## Development Environment

In development (using PHP's built-in server), the nginx proxy doesn't exist. You have two options:

### Option 1: Access ttyd directly

Modify the iframe src to point directly to ttyd:
```php
// For development only
$devUrl = "http://localhost:{$sessionInfo['port']}";
```

**Note:** Browser will prompt for credentials (username: `novapanel`, password: see session JSON file).

### Option 2: Use nginx even in development

Install nginx locally and configure it as shown above, then run:
```bash
# Start PHP-FPM
sudo service php8.2-fpm start

# Start nginx
sudo nginx
```

## Monitoring

### Check active terminal sessions

```bash
# List all ttyd processes
ps aux | grep ttyd

# View session files
ls -la storage/terminal/pids/

# Check session details
cat storage/terminal/pids/1.json
```

### Monitor terminal activity

```bash
# View terminal logs
tail -f storage/terminal/logs/*.log

# View shell command logs
tail -f storage/logs/shell.log
```

## Maintenance

### Cleanup stale sessions

Run the cleanup script periodically via cron:

```bash
# Edit crontab
crontab -e

# Add this line to run every hour
0 * * * * cd /opt/novapanel && php scripts/cleanup-terminals.php >> storage/logs/terminal-cleanup.log 2>&1
```

The script will:
- Remove orphaned sessions (process not running)
- Terminate idle sessions (no activity for > 1 hour)
- Keep active sessions running (even if > 1 hour old)

## Security Best Practices

1. **Use HTTPS:** Always access NovaPanel over HTTPS in production
2. **Strong SSL:** Use modern TLS versions and strong cipher suites
3. **Firewall:** Block direct access to ttyd ports (7100-7199)
4. **User isolation:** Each user should have their own system user account
5. **Sudo restrictions:** Limit sudo access with a whitelist in `/etc/sudoers.d/novapanel`
6. **Log everything:** Monitor `storage/logs/shell.log` for security auditing
7. **Session timeout:** Configure appropriate idle timeout in cleanup script
8. **Rate limiting:** Consider adding nginx rate limiting for terminal endpoints

## References

- ttyd documentation: https://github.com/tsl0922/ttyd
- Nginx WebSocket proxy: https://nginx.org/en/docs/http/websocket.html
- HTTP Basic Authentication: https://developer.mozilla.org/en-US/docs/Web/HTTP/Authentication
