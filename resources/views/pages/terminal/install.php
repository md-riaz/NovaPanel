<?php include __DIR__ . '/../../layouts/app.php'; ?>

<div class="container-fluid">
    <div class="row">
        <?php include __DIR__ . '/../../partials/sidebar.php'; ?>
        
        <main class="col-md-9 ms-sm-auto col-lg-10 px-md-4">
            <div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pt-3 pb-2 mb-3 border-bottom">
                <h1 class="h2">Terminal - Installation Required</h1>
            </div>
            
            <div class="row">
                <div class="col-12">
                    <div class="alert alert-warning">
                        <h4><i class="bi bi-exclamation-triangle"></i> ttyd Not Installed</h4>
                        <p>The terminal feature requires <strong>ttyd</strong> to be installed on your system.</p>
                        <p>ttyd is a simple terminal sharing tool that provides web-based terminal access.</p>
                    </div>
                </div>
            </div>
            
            <div class="row">
                <div class="col-12">
                    <div class="card">
                        <div class="card-header bg-primary text-white">
                            <h5><i class="bi bi-download"></i> Installation Instructions</h5>
                        </div>
                        <div class="card-body">
                            <pre class="bg-dark text-light p-3 rounded" style="overflow-x: auto;"><?= htmlspecialchars($instructions) ?></pre>
                        </div>
                    </div>
                </div>
            </div>
            
            <div class="row mt-3">
                <div class="col-12">
                    <div class="card">
                        <div class="card-header">
                            <h5><i class="bi bi-info-circle"></i> About ttyd</h5>
                        </div>
                        <div class="card-body">
                            <p><strong>ttyd</strong> is a lightweight, open-source tool that shares your terminal over the web using WebSocket.</p>
                            
                            <h6>Features:</h6>
                            <ul>
                                <li>Built on top of Libwebsockets with libuv</li>
                                <li>Fully-featured terminal based on Xterm.js with CJK and IME support</li>
                                <li>SSL support based on OpenSSL</li>
                                <li>Run any custom command with options</li>
                                <li>Basic authentication support</li>
                                <li>Cross-platform: macOS, Linux, FreeBSD, OpenBSD, Windows</li>
                            </ul>
                            
                            <h6>Security:</h6>
                            <ul>
                                <li>NovaPanel runs ttyd sessions with credential authentication</li>
                                <li>Each user gets an isolated terminal session</li>
                                <li>All commands run as the novapanel system user</li>
                                <li>Sessions are automatically terminated on timeout</li>
                            </ul>
                            
                            <h6>Resources:</h6>
                            <ul>
                                <li>GitHub: <a href="https://github.com/tsl0922/ttyd" target="_blank">https://github.com/tsl0922/ttyd</a></li>
                                <li>Documentation: <a href="https://tsl0922.github.io/ttyd/" target="_blank">https://tsl0922.github.io/ttyd/</a></li>
                            </ul>
                        </div>
                    </div>
                </div>
            </div>
            
            <div class="row mt-3">
                <div class="col-12">
                    <div class="alert alert-info">
                        <i class="bi bi-shield-check"></i> 
                        <strong>Note:</strong> After installing ttyd, you may need to restart the NovaPanel web service or refresh this page.
                    </div>
                </div>
            </div>
            
            <div class="row mt-3">
                <div class="col-12">
                    <a href="/dashboard" class="btn btn-secondary">
                        <i class="bi bi-arrow-left"></i> Back to Dashboard
                    </a>
                    <button type="button" class="btn btn-primary" onclick="location.reload()">
                        <i class="bi bi-arrow-clockwise"></i> Check Again
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
</style>
