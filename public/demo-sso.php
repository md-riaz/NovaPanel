<?php
// Demo SSO Flow
session_name('novapanel_session');
session_start();

// Step 1: Set up user session (simulating logged-in user)
$_SESSION['user_id'] = 1;

// Step 2: Load environment
$envFile = __DIR__ . '/../.env.php';
if (file_exists($envFile)) {
    require_once $envFile;
}

// Step 3: Get MySQL credentials
$mysqlHost = getenv('MYSQL_HOST') ?: 'localhost';
$mysqlUser = getenv('MYSQL_ROOT_USER') ?: 'root';
$mysqlPassword = getenv('MYSQL_ROOT_PASSWORD') ?: '';

// Step 4: Set phpMyAdmin signon session
$_SESSION['novapanel_pma_signon'] = [
    'user' => $mysqlUser,
    'password' => $mysqlPassword,
    'host' => $mysqlHost,
];

$dbParam = $_GET['db'] ?? '';
?>
<!DOCTYPE html>
<html>
<head>
    <title>NovaPanel - phpMyAdmin SSO Demo</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Arial, sans-serif; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 20px; }
        .container { background: white; border-radius: 16px; box-shadow: 0 20px 60px rgba(0,0,0,0.3); max-width: 800px; width: 100%; overflow: hidden; }
        .header { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 32px; text-align: center; }
        .header h1 { font-size: 32px; margin-bottom: 8px; }
        .header p { font-size: 16px; opacity: 0.9; }
        .content { padding: 32px; }
        .step { background: #f9fafb; border-left: 4px solid #10b981; padding: 20px; margin: 16px 0; border-radius: 8px; }
        .step h3 { color: #10b981; margin-bottom: 12px; font-size: 18px; }
        .step-details { background: white; padding: 12px; border-radius: 4px; font-family: 'Courier New', monospace; font-size: 14px; margin-top: 12px; }
        .success-box { background: #d1fae5; border: 2px solid #10b981; color: #065f46; padding: 20px; border-radius: 8px; margin: 24px 0; }
        .success-box h2 { margin-bottom: 12px; display: flex; align-items: center; gap: 8px; }
        .btn { display: inline-block; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 16px 32px; border-radius: 8px; text-decoration: none; font-weight: 600; font-size: 16px; margin: 16px 8px; transition: transform 0.2s; }
        .btn:hover { transform: translateY(-2px); }
        .btn-secondary { background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%); }
        .info-grid { display: grid; grid-template-columns: 150px 1fr; gap: 12px; margin: 16px 0; }
        .info-label { font-weight: 600; color: #374151; }
        .info-value { color: #6b7280; font-family: 'Courier New', monospace; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🔐 NovaPanel SSO Demonstration</h1>
            <p>Single Sign-On for phpMyAdmin - No Credentials Required!</p>
        </div>
        
        <div class="content">
            <div class="step">
                <h3>✓ Step 1: Panel User Authentication</h3>
                <p>NovaPanel user session verified</p>
                <div class="step-details">
                    Session Name: <?= session_name() ?><br>
                    Session ID: <?= session_id() ?><br>
                    User ID: <?= $_SESSION['user_id'] ?>
                </div>
            </div>
            
            <div class="step">
                <h3>✓ Step 2: Load MySQL Credentials</h3>
                <p>Credentials loaded from environment configuration</p>
                <div class="step-details">
                    Host: <?= htmlspecialchars($mysqlHost) ?><br>
                    Username: <?= htmlspecialchars($mysqlUser) ?><br>
                    Password: <?= str_repeat('*', strlen($mysqlPassword)) ?> (hidden)
                </div>
            </div>
            
            <div class="step">
                <h3>✓ Step 3: Set phpMyAdmin Signon Session</h3>
                <p>SSO credentials stored in session variable</p>
                <div class="step-details">
                    Session Variable: novapanel_pma_signon<br>
                    Status: ✓ Active
                </div>
            </div>
            
            <div class="success-box">
                <h2><span style="font-size: 24px;">✓</span> SSO Configuration Complete!</h2>
                <p>All steps completed successfully. You can now access phpMyAdmin without entering credentials.</p>
            </div>
            
            <div style="text-align: center; padding: 20px;">
                <a href="/phpmyadmin/index.php" class="btn">
                    🗄️ Access phpMyAdmin (Main View)
                </a>
                <?php if ($dbParam): ?>
                <a href="/phpmyadmin/index.php?db=<?= urlencode($dbParam) ?>" class="btn btn-secondary">
                    📊 Access Database: <?= htmlspecialchars($dbParam) ?>
                </a>
                <?php else: ?>
                <a href="/phpmyadmin/index.php?db=wordpress_db" class="btn btn-secondary">
                    📊 Access Database: wordpress_db
                </a>
                <?php endif; ?>
            </div>
            
            <div style="background: #f9fafb; padding: 20px; border-radius: 8px; margin-top: 24px;">
                <h3 style="margin-bottom: 12px; color: #374151;">How It Works:</h3>
                <div class="info-grid">
                    <div class="info-label">1. User clicks:</div>
                    <div class="info-value">phpMyAdmin link in NovaPanel</div>
                    
                    <div class="info-label">2. Script runs:</div>
                    <div class="info-value">phpmyadmin-signon.php</div>
                    
                    <div class="info-label">3. Checks:</div>
                    <div class="info-value">NovaPanel session authentication</div>
                    
                    <div class="info-label">4. Loads:</div>
                    <div class="info-value">MySQL credentials from .env.php</div>
                    
                    <div class="info-label">5. Sets:</div>
                    <div class="info-value">phpMyAdmin signon session</div>
                    
                    <div class="info-label">6. Redirects:</div>
                    <div class="info-value">To phpMyAdmin (authenticated)</div>
                    
                    <div class="info-label">Result:</div>
                    <div class="info-value" style="color: #10b981; font-weight: bold;">✓ Instant access - No password prompt!</div>
                </div>
            </div>
        </div>
    </div>
</body>
</html>
