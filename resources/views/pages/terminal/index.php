<?php include __DIR__ . '/../../layouts/app.php'; ?>

<div class="container-fluid">
    <div class="row">
        <?php include __DIR__ . '/../../partials/sidebar.php'; ?>
        
        <main class="col-md-9 ms-sm-auto col-lg-10 px-md-4">
            <div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pt-3 pb-2 mb-3 border-bottom">
                <h1 class="h2">Terminal</h1>
                <div class="btn-toolbar mb-2 mb-md-0">
                    <button type="button" class="btn btn-sm btn-outline-secondary me-2" onclick="restartTerminal()">
                        <i class="bi bi-arrow-clockwise"></i> Restart
                    </button>
                    <button type="button" class="btn btn-sm btn-outline-danger" onclick="stopTerminal()">
                        <i class="bi bi-x-circle"></i> Stop Session
                    </button>
                </div>
            </div>
            
            <div class="row">
                <div class="col-12">
                    <div class="card shadow-sm">
                        <div class="card-body p-0">
                            <div id="terminal-container" style="height: 70vh; width: 100%; border: 1px solid #dee2e6;">
                                <iframe 
                                    id="terminal-frame"
                                    src="<?= htmlspecialchars($sessionInfo['url']) ?>"
                                    style="width: 100%; height: 100%; border: none;"
                                    allow="clipboard-read; clipboard-write"
                                ></iframe>
                            </div>
                        </div>
                        <div class="card-footer bg-light">
                            <div class="row">
                                <div class="col-md-6">
                                    <small class="text-muted">
                                        <i class="bi bi-circle-fill text-success"></i> 
                                        Session Active on Port <?= htmlspecialchars($sessionInfo['port']) ?>
                                    </small>
                                </div>
                                <div class="col-md-6 text-end">
                                    <small class="text-muted">
                                        <i class="bi bi-info-circle"></i> 
                                        All commands run as the <code>novapanel</code> user
                                    </small>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
            
            <div class="row mt-3">
                <div class="col-12">
                    <div class="alert alert-info">
                        <h5><i class="bi bi-shield-check"></i> Security Notice</h5>
                        <ul class="mb-0">
                            <li>This terminal runs as the <strong>novapanel</strong> system user</li>
                            <li>You have limited sudo access for approved panel operations only</li>
                            <li>All commands are logged for security auditing</li>
                            <li>Terminal sessions automatically timeout after inactivity</li>
                        </ul>
                    </div>
                </div>
            </div>
            
            <div class="row mt-3">
                <div class="col-12">
                    <div class="card">
                        <div class="card-header">
                            <h5><i class="bi bi-lightbulb"></i> Quick Tips</h5>
                        </div>
                        <div class="card-body">
                            <div class="row">
                                <div class="col-md-6">
                                    <h6>Common Tasks:</h6>
                                    <ul>
                                        <li>Check site files: <code>ls -la /opt/novapanel/sites/</code></li>
                                        <li>View logs: <code>tail -f /opt/novapanel/storage/logs/shell.log</code></li>
                                        <li>Check Nginx status: <code>sudo systemctl status nginx</code></li>
                                        <li>Test Nginx config: <code>sudo nginx -t</code></li>
                                    </ul>
                                </div>
                                <div class="col-md-6">
                                    <h6>Keyboard Shortcuts:</h6>
                                    <ul>
                                        <li><kbd>Ctrl+C</kbd> - Cancel current command</li>
                                        <li><kbd>Ctrl+D</kbd> - Exit shell (will restart session)</li>
                                        <li><kbd>Ctrl+L</kbd> - Clear screen</li>
                                        <li><kbd>Tab</kbd> - Auto-complete commands and paths</li>
                                    </ul>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </main>
    </div>
</div>

<script>
function restartTerminal() {
    if (!confirm('Are you sure you want to restart the terminal session?')) {
        return;
    }
    
    fetch('/terminal/restart', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        }
    })
    .then(response => response.json())
    .then(data => {
        if (data.success) {
            alert('Terminal session restarted successfully');
            location.reload();
        } else {
            alert('Error: ' + (data.error || 'Failed to restart terminal'));
        }
    })
    .catch(error => {
        alert('Error: ' + error.message);
    });
}

function stopTerminal() {
    if (!confirm('Are you sure you want to stop the terminal session?')) {
        return;
    }
    
    fetch('/terminal/stop', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        }
    })
    .then(response => response.json())
    .then(data => {
        if (data.success) {
            alert('Terminal session stopped');
            window.location.href = '/dashboard';
        } else {
            alert('Error: ' + (data.error || 'Failed to stop terminal'));
        }
    })
    .catch(error => {
        alert('Error: ' + error.message);
    });
}

// Check session status periodically
setInterval(function() {
    fetch('/terminal/status')
        .then(response => response.json())
        .then(data => {
            if (!data.active) {
                alert('Terminal session has ended. Reloading page...');
                location.reload();
            }
        })
        .catch(error => {
            console.error('Failed to check terminal status:', error);
        });
}, 30000); // Check every 30 seconds
</script>

<style>
#terminal-container {
    background-color: #1e1e1e;
}

.card {
    border-radius: 0.5rem;
}

.card-footer {
    border-top: 1px solid #dee2e6;
}

kbd {
    background-color: #333;
    color: #fff;
    padding: 2px 6px;
    border-radius: 3px;
    font-family: monospace;
    font-size: 0.9em;
}

code {
    background-color: #f8f9fa;
    padding: 2px 6px;
    border-radius: 3px;
    font-family: 'Courier New', monospace;
}
</style>
