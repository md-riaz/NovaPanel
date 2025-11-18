<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Databases</h1>
    <div>
        <a href="/phpmyadmin-signon.php" class="btn btn-success me-2" target="_blank">
            <i class="bi bi-box-arrow-up-right"></i> phpMyAdmin
        </a>
        <a href="/databases/create" class="btn btn-primary">
            <i class="bi bi-plus-circle"></i> Create Database
        </a>
    </div>
</div>

<div class="alert alert-info">
    <i class="bi bi-info-circle"></i>
    <strong>phpMyAdmin Access:</strong> Click the "phpMyAdmin" button above to access phpMyAdmin. You will be automatically logged in and can view, edit, and manage your MySQL databases through a user-friendly interface.
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
                        <td>
                            <strong><?= htmlspecialchars($db->name) ?></strong>
                        </td>
                        <td>
                            <span class="badge bg-<?= $db->type === 'mysql' ? 'primary' : 'info' ?>">
                                <?= strtoupper(htmlspecialchars($db->type)) ?>
                            </span>
                        </td>
                        <td><?= htmlspecialchars($db->ownerUsername ?? 'Unknown') ?></td>
                        <td><?= date('Y-m-d H:i', strtotime($db->createdAt)) ?></td>
                        <td>
                            <a href="/phpmyadmin-signon.php?db=<?= urlencode($db->name) ?>" 
                               class="btn btn-sm btn-outline-primary" 
                               target="_blank"
                               title="Manage with phpMyAdmin">
                                <i class="bi bi-pencil-square"></i> Manage
                            </a>
                            <form action="/databases/<?= $db->id ?>/delete" method="POST" style="display: inline;" 
                                  onsubmit="return confirm('Are you sure you want to delete this database? This action cannot be undone!');">
                                <button type="submit" class="btn btn-sm btn-outline-danger">
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
