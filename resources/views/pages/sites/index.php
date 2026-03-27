<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Sites</h1>
    <a href="/sites/create" class="btn btn-primary">
        <i class="bi bi-plus-circle"></i> Create Site
    </a>
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover align-middle">
        <thead>
            <tr>
                <th>Domain</th>
                <th>Owner</th>
                <th>PHP</th>
                <th>Certificate</th>
                <th>Renewal</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            <?php if (empty($sites)): ?>
                <tr>
                    <td colspan="6" class="text-center text-muted">No sites found</td>
                </tr>
            <?php else: ?>
                <?php foreach ($sites as $site): ?>
                    <tr>
                        <td>
                            <strong><?= htmlspecialchars($site->domain) ?></strong>
                            <div class="small text-muted"><?= htmlspecialchars($site->documentRoot) ?></div>
                        </td>
                        <td><i class="bi bi-person"></i> <?= htmlspecialchars($site->ownerUsername ?? ('User #' . $site->userId)) ?></td>
                        <td><span class="badge bg-info">PHP <?= htmlspecialchars($site->phpVersion) ?></span></td>
                        <td>
                            <?php
                                $statusClass = match ($site->certificateStatus) {
                                    'active' => 'success',
                                    'pending', 'renewing' => 'warning text-dark',
                                    'failed', 'revoked' => 'danger',
                                    default => 'secondary',
                                };
                            ?>
                            <span class="badge bg-<?= $statusClass ?>"><?= htmlspecialchars(ucfirst($site->certificateStatus ?? 'unissued')) ?></span>
                            <div class="small text-muted mt-1">
                                <?= htmlspecialchars(strtoupper($site->certificateValidationMethod ?? 'webroot')) ?>
                                · <?= $site->forceHttps ? 'Force HTTPS' : 'HTTP + HTTPS' ?>
                            </div>
                            <?php if (!empty($site->lastCertificateError)): ?>
                                <div class="small text-danger mt-1"><?= htmlspecialchars($site->lastCertificateError) ?></div>
                            <?php endif; ?>
                        </td>
                        <td>
                            <div class="small">
                                Auto renew: <strong><?= $site->certificateAutoRenew ? 'On' : 'Off' ?></strong>
                            </div>
                            <div class="small text-muted">
                                Expires: <?= htmlspecialchars($site->certificateExpiresAt ?: 'Not installed') ?>
                            </div>
                        </td>
                        <td>
                            <div class="btn-group btn-group-sm" role="group">
                                <a href="/sites/<?= $site->id ?>" class="btn btn-outline-primary">Manage</a>
                                <form method="POST" action="/sites/<?= $site->id ?>/certificate/renew" class="d-inline">
                                    <button type="submit" class="btn btn-outline-success">Renew</button>
                                </form>
                            </div>
                        </td>
                    </tr>
                <?php endforeach; ?>
            <?php endif; ?>
        </tbody>
    </table>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
