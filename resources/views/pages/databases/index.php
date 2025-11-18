<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Databases</h1>
    <div>
        <a href="/db-access.php" target="_blank" class="btn btn-success me-2" title="Open database management interface">
            <i class="bi bi-database-gear"></i> Database Manager
        </a>
        <a href="/databases/create" class="btn btn-primary">
            <i class="bi bi-plus-circle"></i> Create Database
        </a>
    </div>
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover">
        <thead>
            <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Account</th>
                <th>Created</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            <?php if (empty($databases)): ?>
                <tr>
                    <td colspan="5" class="text-center text-muted">No databases found</td>
                </tr>
            <?php else: ?>
                <?php foreach ($databases as $db): ?>
                    <tr>
                        <td><?= htmlspecialchars($db->name) ?></td>
                        <td><span class="badge bg-info"><?= htmlspecialchars(strtoupper($db->type)) ?></span></td>
                        <td><?= htmlspecialchars($db->ownerUsername ?? 'Unknown') ?></td>
                        <td><?= htmlspecialchars($db->createdAt ?? 'N/A') ?></td>
                        <td>
                            <a href="/db-access.php?db=<?= urlencode($db->name) ?>" 
                               target="_blank" 
                               class="btn btn-sm btn-primary" 
                               title="Access this database">
                                <i class="bi bi-box-arrow-up-right"></i> Access
                            </a>
                            <form method="POST" action="/databases/<?= $db->id ?>/delete" style="display: inline;">
                                <button type="submit" class="btn btn-sm btn-danger" 
                                        onclick="return confirm('Are you sure you want to delete this database?')">
                                    <i class="bi bi-trash"></i> Delete
                                </button>
                            </form>
                        </td>
                    </tr>
                <?php endforeach; ?>
            <?php endif; ?>
        </tbody>
    </table>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
