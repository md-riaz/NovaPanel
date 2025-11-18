<?php
session_name('novapanel_session');
session_start();

$authenticated = isset($_SESSION['novapanel_pma_signon']);
$credentials = $_SESSION['novapanel_pma_signon'] ?? null;
$selectedDb = $_GET['db'] ?? null;
?>
<!DOCTYPE html>
<html>
<head>
    <title>phpMyAdmin</title>
    <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>🗄️</text></svg>">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Arial, sans-serif; background: #f5f5f5; }
        .header { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 12px 20px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); display: flex; align-items: center; justify-content: space-between; }
        .logo { font-size: 22px; font-weight: 600; display: flex; align-items: center; gap: 10px; }
        .logo::before { content: '🗄️'; font-size: 28px; }
        .sso-badge { background: #10b981; padding: 6px 14px; border-radius: 20px; font-size: 13px; font-weight: 600; display: flex; align-items: center; gap: 6px; }
        .sso-badge::before { content: '✓'; font-size: 16px; }
        .container { display: flex; height: calc(100vh - 56px); }
        .sidebar { width: 280px; background: white; border-right: 1px solid #e5e7eb; overflow-y: auto; }
        .main { flex: 1; padding: 24px; overflow-y: auto; }
        .server-box { padding: 16px; background: #f9fafb; border-bottom: 1px solid #e5e7eb; }
        .server-box h3 { font-size: 15px; color: #374151; margin-bottom: 8px; }
        .server-info { font-size: 13px; color: #6b7280; margin: 4px 0; }
        .db-list { list-style: none; }
        .db-item { padding: 12px 16px; border-bottom: 1px solid #f3f4f6; cursor: pointer; display: flex; align-items: center; gap: 10px; transition: all 0.2s; }
        .db-item:hover { background: #f9fafb; }
        .db-item.active { background: #eff6ff; border-left: 3px solid #3b82f6; font-weight: 500; }
        .db-icon { width: 20px; height: 20px; background: linear-gradient(135deg, #10b981, #059669); border-radius: 4px; display: flex; align-items: center; justify-content: center; color: white; font-size: 11px; font-weight: bold; }
        .alert { padding: 16px 20px; border-radius: 8px; margin-bottom: 20px; display: flex; align-items: start; gap: 12px; }
        .alert-success { background: #d1fae5; border: 1px solid #a7f3d0; color: #065f46; }
        .alert-icon { font-size: 24px; }
        .alert-content h4 { font-size: 16px; font-weight: 600; margin-bottom: 4px; }
        .alert-content p { font-size: 14px; opacity: 0.9; }
        .card { background: white; border-radius: 8px; padding: 20px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); margin-bottom: 20px; }
        .card h3 { font-size: 18px; color: #111827; margin-bottom: 16px; font-weight: 600; }
        .detail-row { padding: 10px 0; border-bottom: 1px solid #f3f4f6; display: flex; }
        .detail-row:last-child { border-bottom: none; }
        .detail-label { font-weight: 600; color: #374151; width: 180px; }
        .detail-value { color: #6b7280; flex: 1; }
        .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin: 20px 0; }
        .stat-card { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
        .stat-label { font-size: 12px; color: #6b7280; text-transform: uppercase; font-weight: 600; letter-spacing: 0.5px; }
        .stat-value { font-size: 32px; font-weight: 700; color: #3b82f6; margin-top: 8px; }
        h1 { font-size: 28px; color: #111827; margin-bottom: 24px; font-weight: 700; }
        h2 { font-size: 20px; color: #374151; margin: 24px 0 16px 0; font-weight: 600; }
        .feature-list { list-style: none; }
        .feature-list li { padding: 12px 0; border-bottom: 1px solid #f3f4f6; display: flex; align-items: center; gap: 12px; color: #374151; }
        .feature-list li::before { content: '✓'; color: #10b981; font-weight: bold; font-size: 20px; }
    </style>
</head>
<body>
    <div class="header">
        <div class="logo">phpMyAdmin <span style="font-size: 13px; opacity: 0.8;">5.2.1</span></div>
        <?php if ($authenticated): ?>
            <div class="sso-badge">SSO AUTHENTICATED</div>
        <?php endif; ?>
    </div>
    
    <div class="container">
        <div class="sidebar">
            <div class="server-box">
                <h3>Server: <?= htmlspecialchars($credentials['host'] ?? 'localhost') ?></h3>
                <div class="server-info"><strong>User:</strong> <?= htmlspecialchars($credentials['user'] ?? 'N/A') ?></div>
                <div class="server-info"><strong>Server:</strong> MySQL 8.0.32</div>
                <div class="server-info"><strong>Protocol:</strong> TCP/IP</div>
            </div>
            
            <ul class="db-list">
                <li class="db-item <?= !$selectedDb ? 'active' : '' ?>">
                    <div class="db-icon">i</div>
                    <span>information_schema</span>
                </li>
                <li class="db-item">
                    <div class="db-icon">M</div>
                    <span>mysql</span>
                </li>
                <li class="db-item">
                    <div class="db-icon">P</div>
                    <span>performance_schema</span>
                </li>
                <li class="db-item">
                    <div class="db-icon">S</div>
                    <span>sys</span>
                </li>
                <li class="db-item <?= $selectedDb == 'wordpress_db' ? 'active' : '' ?>">
                    <div class="db-icon">W</div>
                    <span>wordpress_db</span>
                </li>
                <li class="db-item <?= $selectedDb == 'laravel_db' ? 'active' : '' ?>">
                    <div class="db-icon">L</div>
                    <span>laravel_db</span>
                </li>
                <li class="db-item <?= $selectedDb == 'ecommerce_db' ? 'active' : '' ?>">
                    <div class="db-icon">E</div>
                    <span>ecommerce_db</span>
                </li>
                <?php if ($selectedDb && !in_array($selectedDb, ['wordpress_db', 'laravel_db', 'ecommerce_db'])): ?>
                <li class="db-item active">
                    <div class="db-icon">D</div>
                    <span><?= htmlspecialchars($selectedDb) ?></span>
                </li>
                <?php endif; ?>
            </ul>
        </div>
        
        <div class="main">
            <h1>Database Server</h1>
            
            <?php if ($authenticated): ?>
            <div class="alert alert-success">
                <div class="alert-icon">✓</div>
                <div class="alert-content">
                    <h4>Successfully Authenticated via NovaPanel SSO!</h4>
                    <p>You were automatically logged in without entering any credentials. This demonstrates the Single Sign-On functionality.</p>
                </div>
            </div>
            
            <div class="card">
                <h3>🔐 SSO Authentication Details</h3>
                <div class="detail-row">
                    <div class="detail-label">MySQL Host:</div>
                    <div class="detail-value"><?= htmlspecialchars($credentials['host']) ?></div>
                </div>
                <div class="detail-row">
                    <div class="detail-label">MySQL Username:</div>
                    <div class="detail-value"><?= htmlspecialchars($credentials['user']) ?></div>
                </div>
                <div class="detail-row">
                    <div class="detail-label">Authentication Method:</div>
                    <div class="detail-value">Single Sign-On (Signon)</div>
                </div>
                <div class="detail-row">
                    <div class="detail-label">Session Variable:</div>
                    <div class="detail-value">novapanel_pma_signon</div>
                </div>
                <div class="detail-row">
                    <div class="detail-label">Panel Session:</div>
                    <div class="detail-value">novapanel_session</div>
                </div>
            </div>
            <?php endif; ?>
            
            <h2>📊 Database Statistics</h2>
            <div class="stats">
                <div class="stat-card">
                    <div class="stat-label">Databases</div>
                    <div class="stat-value">7</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">Tables</div>
                    <div class="stat-value">48</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">Total Size</div>
                    <div class="stat-value">156 <span style="font-size: 18px;">MB</span></div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">Active Connections</div>
                    <div class="stat-value">4</div>
                </div>
            </div>
            
            <?php if ($selectedDb): ?>
            <div class="alert alert-success">
                <div class="alert-icon">🎯</div>
                <div class="alert-content">
                    <h4>Database Pre-selected: <?= htmlspecialchars($selectedDb) ?></h4>
                    <p>The database was automatically selected based on the "Manage" link you clicked in NovaPanel.</p>
                </div>
            </div>
            <?php endif; ?>
            
            <div class="card">
                <h3>✨ Available Features</h3>
                <ul class="feature-list">
                    <li>Browse databases and tables</li>
                    <li>Execute SQL queries with syntax highlighting</li>
                    <li>Import and export data (CSV, SQL, XML, JSON)</li>
                    <li>Manage users and permissions</li>
                    <li>Create and modify table structures</li>
                    <li>View server status and variables</li>
                    <li>Database search and operations</li>
                    <li>Foreign key relationships</li>
                </ul>
            </div>
        </div>
    </div>
</body>
</html>
