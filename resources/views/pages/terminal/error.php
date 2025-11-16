<?php include __DIR__ . '/../../layouts/app.php'; ?>

<div class="container-fluid">
    <div class="row">
        <?php include __DIR__ . '/../../partials/sidebar.php'; ?>
        
        <main class="col-md-9 ms-sm-auto col-lg-10 px-md-4">
            <div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pt-3 pb-2 mb-3 border-bottom">
                <h1 class="h2">Terminal - Error</h1>
            </div>
            
            <div class="row">
                <div class="col-12">
                    <div class="alert alert-danger">
                        <h4><i class="bi bi-exclamation-triangle-fill"></i> Terminal Session Error</h4>
                        <p><strong>Error:</strong> <?= htmlspecialchars($error) ?></p>
                    </div>
                </div>
            </div>
            
            <div class="row">
                <div class="col-12">
                    <div class="card">
                        <div class="card-header">
                            <h5><i class="bi bi-wrench"></i> Troubleshooting</h5>
                        </div>
                        <div class="card-body">
                            <h6>Possible Causes:</h6>
                            <ol>
                                <li><strong>ttyd not installed</strong> - Follow the installation instructions below</li>
                                <li><strong>Port already in use</strong> - Another service may be using the terminal port</li>
                                <li><strong>Permission issues</strong> - The novapanel user may not have permission to start ttyd</li>
                                <li><strong>Process limit reached</strong> - Too many terminal sessions may be running</li>
                            </ol>
                            
                            <h6>Solutions:</h6>
                            <ul>
                                <li>Ensure ttyd is installed (see instructions below)</li>
                                <li>Check if the process is running: <code>ps aux | grep ttyd</code></li>
                                <li>Check available ports: <code>netstat -tulpn | grep 71</code></li>
                                <li>Review the logs: <code>/opt/novapanel/storage/terminal/logs/</code></li>
                                <li>Try restarting the terminal session from the dashboard</li>
                            </ul>
                        </div>
                    </div>
                </div>
            </div>
            
            <div class="row mt-3">
                <div class="col-12">
                    <div class="card">
                        <div class="card-header bg-primary text-white">
                            <h5><i class="bi bi-download"></i> ttyd Installation Instructions</h5>
                        </div>
                        <div class="card-body">
                            <pre class="bg-dark text-light p-3 rounded" style="overflow-x: auto;"><?= htmlspecialchars($instructions) ?></pre>
                        </div>
                    </div>
                </div>
            </div>
            
            <div class="row mt-3">
                <div class="col-12">
                    <a href="/dashboard" class="btn btn-secondary">
                        <i class="bi bi-arrow-left"></i> Back to Dashboard
                    </a>
                    <button type="button" class="btn btn-primary" onclick="location.reload()">
                        <i class="bi bi-arrow-clockwise"></i> Try Again
                    </button>
                </div>
            </div>
        </main>
    </div>
</div>

<style>
pre {
    white-space: pre-wrap;
    word-wrap: break-word;
}

code {
    background-color: #f8f9fa;
    padding: 2px 6px;
    border-radius: 3px;
    font-family: 'Courier New', monospace;
}
</style>
