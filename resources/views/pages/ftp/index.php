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
            <tr>
                <td colspan="6" class="text-center text-muted">No FTP users found</td>
            </tr>
        </tbody>
    </table>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
