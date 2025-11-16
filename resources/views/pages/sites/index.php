<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Sites</h1>
    <a href="/sites/create" class="btn btn-primary">
        <i class="bi bi-plus-circle"></i> Create Site
    </a>
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover">
        <thead>
            <tr>
                <th>Domain</th>
                <th>Owner</th>
                <th>PHP Version</th>
                <th>SSL</th>
                <th>Created</th>
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
                        <td><strong><?= htmlspecialchars($site->domain) ?></strong></td>
                        <td>
                            <?php if (isset($site->ownerUsername)): ?>
                                <i class="bi bi-person"></i> <?= htmlspecialchars($site->ownerUsername) ?>
                            <?php else: ?>
                                User #<?= $site->userId ?>
                            <?php endif; ?>
                        </td>
                        <td><span class="badge bg-info">PHP <?= htmlspecialchars($site->phpVersion) ?></span></td>
                        <td>
                            <?php if ($site->sslEnabled): ?>
                                <span class="badge bg-success"><i class="bi bi-lock-fill"></i> Enabled</span>
                            <?php else: ?>
                                <span class="badge bg-secondary"><i class="bi bi-unlock"></i> Disabled</span>
                            <?php endif; ?>
                        </td>
                        <td><?= htmlspecialchars($site->createdAt) ?></td>
                        <td>
                            <a href="/sites/<?= $site->id ?>" class="btn btn-sm btn-info">View</a>
                            <a href="/sites/<?= $site->id ?>/edit" class="btn btn-sm btn-warning">Edit</a>
                        </td>
                    </tr>
                <?php endforeach; ?>
            <?php endif; ?>
        </tbody>
    </table>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
