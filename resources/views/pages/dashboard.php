<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Dashboard</h1>
    <button class="btn btn-sm btn-outline-secondary"
            hx-get="/dashboard/stats"
            hx-target="#stats-container"
            hx-swap="innerHTML">
        <i class="bi bi-arrow-clockwise"></i> Refresh Stats
    </button>
</div>

<div class="row" id="stats-container" hx-get="/dashboard/stats" hx-trigger="load, every 30s" hx-swap="innerHTML">
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-secondary">
            <div class="card-body">
                <h5 class="card-title">Accounts</h5>
                <p class="card-text display-4">
                    <span class="spinner-border spinner-border-sm" role="status"></span>
                </p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-secondary">
            <div class="card-body">
                <h5 class="card-title">Sites</h5>
                <p class="card-text display-4">
                    <span class="spinner-border spinner-border-sm" role="status"></span>
                </p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-secondary">
            <div class="card-body">
                <h5 class="card-title">Databases</h5>
                <p class="card-text display-4">
                    <span class="spinner-border spinner-border-sm" role="status"></span>
                </p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-secondary">
            <div class="card-body">
                <h5 class="card-title">FTP Users</h5>
                <p class="card-text display-4">
                    <span class="spinner-border spinner-border-sm" role="status"></span>
                </p>
            </div>
        </div>
    </div>
</div>

<?php if (!empty($certificateFailures ?? [])): ?>
    <div class="alert alert-warning mt-4">
        <div class="d-flex justify-content-between align-items-center flex-wrap gap-2">
            <div>
                <strong>Certificate renewals need attention.</strong>
                <div class="small">Failures are also logged to <code>storage/logs/certificates.log</code>.</div>
            </div>
            <a href="/sites" class="btn btn-sm btn-outline-dark">Review Sites</a>
        </div>
        <ul class="mt-3 mb-0">
            <?php foreach ($certificateFailures as $failedSite): ?>
                <li>
                    <strong><?= htmlspecialchars($failedSite['domain']) ?></strong>
                    (<?= htmlspecialchars($failedSite['ownerUsername'] ?? 'Unknown') ?>)
                    — <?= htmlspecialchars($failedSite['lastCertificateError']) ?>
                </li>
            <?php endforeach; ?>
        </ul>
    </div>
<?php endif; ?>

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
