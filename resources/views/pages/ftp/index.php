<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">FTP Users</h1>
    <a href="/ftp/create" class="btn btn-primary">
        <i class="bi bi-plus-circle"></i> Create FTP User
    </a>
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover">
        <thead>
            <tr>
                <th>Username</th>
                <th>Account</th>
                <th>Home Directory</th>
                <th>Status</th>
                <th>Created</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
                <?php if (!empty($ftpUsers)) {
                    foreach ($ftpUsers as $ftpUser) {
                ?>
                        <tr>
                            <td><?= htmlspecialchars($ftpUser->username) ?></td>
                            <td><?= htmlspecialchars($ftpUser->ownerUsername ?? '') ?></td>
                            <td><?= htmlspecialchars($ftpUser->homeDirectory) ?></td>
                            <td>
                                <?php if ($ftpUser->enabled) { ?>
                                    <span class="badge bg-success">Enabled</span>
                                <?php } else { ?>
                                    <span class="badge bg-danger">Disabled</span>
                                <?php } ?>
                            </td>
                            <td><?= htmlspecialchars($ftpUser->createdAt) ?></td>
                            <td>
                                <form method="POST" action="/ftp/<?= $ftpUser->id ?>/delete" style="display: inline;"
                                      onsubmit="return confirm('Are you sure you want to delete this FTP user?');">
                                    <button type="submit" class="btn btn-sm btn-danger">
                                        <i class="bi bi-trash"></i> Delete
                                    </button>
                                </form>
                            </td>
                        </tr>
                <?php
                    }
                } else {
                ?>
                    <tr>
                        <td colspan="6" class="text-center text-muted">No FTP users found</td>
                    </tr>
                <?php } ?>
        </tbody>
    </table>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
