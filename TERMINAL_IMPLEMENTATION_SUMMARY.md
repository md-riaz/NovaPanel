
## End-to-End Secure Terminal Integration (ttyd + PHP + Nginx)

### 1. Mental Model (What You’re Building)

**Flow:**
1. User logs into your **PHP panel** → normal `$_SESSION` auth.
2. User clicks **“Terminal”** → goes to `terminal.php`.
3. `terminal.php` shows an `<iframe src="/ttyd/">`.
4. Browser requests `/ttyd/`.
5. **Nginx** calls a hidden PHP route (`/auth_check.php`) to ask: “Is this user logged in?”
6. If **yes** → Nginx proxies to ttyd on `127.0.0.1:7681`. If **no** → Nginx redirects to `login.php`.

**Result:**
* One login.
* No extra password.
* Nginx + PHP decide who is allowed to touch ttyd.
* ttyd is just a local shell engine.

---

### 2. PHP Side

#### 2.1. Login (simplified example)
```php
// login.php
session_start();
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
   $username = $_POST['username'] ?? '';
   $password = $_POST['password'] ?? '';
   // TODO: check from DB/users table
   if ($username === 'customer1' && $password === 'MyStrongPass') {
      $_SESSION['user_id'] = 1;
      $_SESSION['username'] = $username;
      header('Location: /dashboard.php');
      exit;
   }
   $error = 'Invalid credentials';
}
?>
<form method="post">
   <input name="username">
   <input name="password" type="password">
   <button type="submit">Login</button>
   <?php if (!empty($error)) echo "<p>$error</p>"; ?>
</form>
```

#### 2.2. Terminal Page (inside panel)
```php
<?php
// terminal.php
session_start();
if (!isset($_SESSION['user_id'])) {
   header('Location: /login.php');
   exit;
}
?>
<!doctype html>
<html>
<head>
   <meta charset="utf-8">
   <title>Web Terminal</title>
   <style>
      html, body { margin: 0; padding: 0; height: 100%; width: 100%; background: #000; }
      iframe { border: 0; width: 100%; height: 100%; }
   </style>
</head>
<body>
   <iframe src="/ttyd/"></iframe>
</body>
</html>
```

#### 2.3. Auth Check Route (used only by Nginx)
```php
<?php
// auth_check.php
session_start();
if (!isset($_SESSION['user_id'])) {
   http_response_code(401);
   exit;
}
// Optional: you can check roles here:
// if ($_SESSION['role'] !== 'admin') { http_response_code(403); exit; }
http_response_code(200);
```
* If logged in → returns **200**.
* If not → returns **401/403**.
* Nginx reads that and decides whether to allow `/ttyd/`.

---

### 3. ttyd Setup

Run ttyd **only on localhost**, no credentials needed:
```bash

```
You can make it a systemd service:
```ini
# /etc/systemd/system/ttyd.service
[Unit]
Description=ttyd web terminal
After=network.target

[Service]
ExecStart=/usr/local/bin/ttyd --interface 127.0.0.1 --port 7681 bash
Restart=always
User=root
WorkingDirectory=/root

[Install]
WantedBy=multi-user.target
```
Then:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ttyd
```

---

### 4. Nginx Config with `auth_request`

Here’s the key piece.
```nginx
server {
   listen 80;
   server_name yourpanel.example.com;
   root /var/www/html;

   # PHP handling for normal app
   location ~ \.php$ {
      include fastcgi_params;
      fastcgi_pass unix:/run/php/php8.2-fpm.sock;
      fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
   }

   # 1) INTERNAL auth check endpoint for Nginx
   location = /auth_check {
      include fastcgi_params;
      fastcgi_pass unix:/run/php/php8.2-fpm.sock;
      fastcgi_param SCRIPT_FILENAME $document_root/auth_check.php;
      # Pass cookies so PHP can read session
      fastcgi_param HTTP_COOKIE $http_cookie;
   }

   # 2) Protected ttyd endpoint
   location /ttyd/ {
      # ask PHP "is user logged in?"
      auth_request /auth_check;
      # If PHP said 401, send them to login page
      error_page 401 = /login.php;
      proxy_pass http://127.0.0.1:7681;
      proxy_http_version 1.1;
      proxy_set_header Upgrade $http_upgrade;
      proxy_set_header Connection "upgrade";
      proxy_set_header Host $host;
      proxy_read_timeout 86400;
   }

   # 3) Static / app routes…
   location / {
      try_files $uri $uri/ /dashboard.php;
   }
}
```

**What happens internally:**
* Browser hits `/ttyd/`.
* Nginx does an **internal subrequest** to `/auth_check`.
* `/auth_check.php` runs with PHP session:
  * If valid → 200.
  * If not → 401.
* If 200 → Nginx proxies to ttyd.
* If 401 → Nginx serves `/login.php` instead.

---

### 5. Customer Experience

From the customer’s perspective:
1. Visit `https://yourpanel.example.com/login.php`.
2. Log in with panel credentials.
3. See dashboard.
4. Click **“Terminal”** → loads `terminal.php`.
5. `terminal.php` shows ttyd inside iframe (`/ttyd/`).
6. No Basic Auth popup, no extra password, just the terminal.

When they log out (destroy `$_SESSION`), `/auth_check.php` starts returning 401 → `/ttyd/` is no longer accessible.

---

### 6. Why this is a solid design

* **No tokens in URL** — all auth is cookie-based (PHP session).
* **Security centralized** — PHP decides who’s logged in; Nginx enforces it.
* **ttyd is never public** — bound to `127.0.0.1`, only reachable through Nginx.
* **Matches your mental model** — exactly how cPanel-style “single login, many tools” works.

Later, you can extend:
* Per-user restricted shells.
* Logging which panel user opened a terminal.
* Idle timeout (kill ttyd if nobody connected for X minutes).

But the core auth model (PHP session + Nginx auth_request + ttyd on localhost) is already a very good foundation.
- **New Components:** 2 (Controller + Adapter)
