<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Accounts</h1>
    <a href="/accounts/create" class="btn btn-primary">
        <i class="bi bi-plus-circle"></i> Create Account
    </a>
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover">
        <thead>
            <tr>
                <th>Username</th>
                <th>Home Directory</th>
                <th>Status</th>
                <th>Created</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            <?php if (empty($accounts)): ?>
                <tr>
                    <td colspan="5" class="text-center text-muted">No accounts found</td>
                </tr>
            <?php else: ?>
                <?php foreach ($accounts as $account): ?>
                    <tr>
                        <td><?= htmlspecialchars($account->username) ?></td>
                        <td><code><?= htmlspecialchars($account->homeDirectory) ?></code></td>
                        <td>
                            <?php if ($account->suspended): ?>
                                <span class="badge bg-danger">Suspended</span>
                            <?php else: ?>
                                <span class="badge bg-success">Active</span>
                            <?php endif; ?>
                        </td>
                        <td><?= htmlspecialchars($account->createdAt) ?></td>
                        <td>
                            <a href="/accounts/<?= $account->id ?>" class="btn btn-sm btn-info">View</a>
                            <a href="/accounts/<?= $account->id ?>/edit" class="btn btn-sm btn-warning">Edit</a>
                        </td>
                    </tr>
                <?php endforeach; ?>
            <?php endif; ?>
        </tbody>
    </table>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
