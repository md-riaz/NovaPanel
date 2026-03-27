<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Dashboard</h1>
    <div class="d-flex gap-2">
        <button class="btn btn-sm btn-outline-secondary"
                hx-get="/dashboard/stats"
                hx-target="#stats-container"
                hx-swap="innerHTML">
            <i class="bi bi-arrow-clockwise"></i> Refresh Stats
        </button>
        <button class="btn btn-sm btn-outline-primary"
                hx-get="/dashboard/system-status"
                hx-target="#system-status-container"
                hx-swap="innerHTML">
            <i class="bi bi-cpu"></i> Refresh Status
        </button>
    </div>
</div>

<div class="row" id="stats-container" hx-get="/dashboard/stats" hx-trigger="load, every 30s" hx-swap="innerHTML">
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-secondary">
            <div class="card-body">
                <h5 class="card-title">Accounts</h5>
                <p class="card-text display-4"><span class="spinner-border spinner-border-sm" role="status"></span></p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-secondary">
            <div class="card-body">
                <h5 class="card-title">Sites</h5>
                <p class="card-text display-4"><span class="spinner-border spinner-border-sm" role="status"></span></p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-secondary">
            <div class="card-body">
                <h5 class="card-title">Databases</h5>
                <p class="card-text display-4"><span class="spinner-border spinner-border-sm" role="status"></span></p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-secondary">
            <div class="card-body">
                <h5 class="card-title">FTP Users</h5>
                <p class="card-text display-4"><span class="spinner-border spinner-border-sm" role="status"></span></p>
            </div>
        </div>
    </div>
</div>

<section class="mt-4">
    <div class="d-flex justify-content-between align-items-center mb-3">
        <div>
            <h3 class="mb-1">System status</h3>
            <p class="text-muted mb-0">Track service health, disk pressure, memory usage, and host load from the dashboard.</p>
        </div>
    </div>
    <div id="system-status-container" hx-get="/dashboard/system-status" hx-trigger="load, every 30s" hx-swap="innerHTML">
        <?php include __DIR__ . '/../partials/widgets/system-status.php'; ?>
    </div>
</section>

<section class="mt-4">
    <h3>Feature wave modules</h3>
    <div class="row g-3">
        <div class="col-md-6 col-xl-3">
            <div class="card h-100 border-0 shadow-sm">
                <div class="card-body">
                    <div class="text-primary mb-2"><i class="bi bi-cpu fs-3"></i></div>
                    <h5>System status</h5>
                    <p class="text-muted">Surface expected panel health signals without leaving the dashboard.</p>
                    <a href="/dashboard/system-status" class="btn btn-sm btn-outline-primary">Refresh widget</a>
                </div>
            </div>
        </div>
        <div class="col-md-6 col-xl-3">
            <div class="card h-100 border-0 shadow-sm">
                <div class="card-body">
                    <div class="text-primary mb-2"><i class="bi bi-journal-text fs-3"></i></div>
                    <h5>Log viewer</h5>
                    <p class="text-muted">Review Nginx, PHP-FPM, audit, and application activity from one place.</p>
                    <a href="/logs" class="btn btn-sm btn-outline-primary">Open logs</a>
                </div>
            </div>
        </div>
        <div class="col-md-6 col-xl-3">
            <div class="card h-100 border-0 shadow-sm">
                <div class="card-body">
                    <div class="text-primary mb-2"><i class="bi bi-shield-check fs-3"></i></div>
                    <h5>Security</h5>
                    <p class="text-muted">Start with read-only firewall visibility, then run safe actions when supported.</p>
                    <a href="/security" class="btn btn-sm btn-outline-primary">Review security</a>
                </div>
            </div>
        </div>
        <div class="col-md-6 col-xl-3">
            <div class="card h-100 border-0 shadow-sm">
                <div class="card-body">
                    <div class="text-primary mb-2"><i class="bi bi-grid-1x2 fs-3"></i></div>
                    <h5>Templates</h5>
                    <p class="text-muted">Ship starter application blueprints independently from deeper site workflow work.</p>
                    <a href="/templates" class="btn btn-sm btn-outline-primary">Browse templates</a>
                </div>
            </div>
        </div>
    </div>
</section>

<div class="row mt-4">
    <div class="col-md-12">
        <h3>Quick Actions</h3>
        <div class="list-group">
            <a href="/users/create" class="list-group-item list-group-item-action">
                <i class="bi bi-plus-circle"></i> Create New User
            </a>
            <a href="/sites/create" class="list-group-item list-group-item-action">
                <i class="bi bi-plus-circle"></i> Create New Site
            </a>
            <a href="/databases/create" class="list-group-item list-group-item-action">
                <i class="bi bi-plus-circle"></i> Create New Database
            </a>
        </div>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../layouts/app.php'; ?>
