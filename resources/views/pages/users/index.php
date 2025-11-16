<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Panel Users</h1>
    <a href="/users/create" class="btn btn-primary">
        <i class="bi bi-plus-circle"></i> Create Panel User
    </a>
</div>

<div class="alert alert-info" role="alert">
    <i class="bi bi-info-circle"></i> 
    <strong>Panel Users</strong> are people who can log into the NovaPanel interface. 
    After creating a panel user, you can create <strong>Accounts</strong> (system-level hosting accounts) and assign them to panel users.
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover">
        <thead>
            <tr>
                <th>Username</th>
                <th>Email</th>
                <th>Roles</th>
                <th>Created</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            <?php if (empty($users)): ?>
                <tr>
                    <td colspan="5" class="text-center text-muted">No panel users found</td>
                </tr>
            <?php else: ?>
                <?php foreach ($users as $user): ?>
                    <tr>
                        <td><strong><?= htmlspecialchars($user->username) ?></strong></td>
                        <td><?= htmlspecialchars($user->email) ?></td>
                        <td>
                            <?php if (!empty($user->roles)): ?>
                                <?php foreach ($user->roles as $role): ?>
                                    <span class="badge bg-primary"><?= htmlspecialchars($role->name) ?></span>
                                <?php endforeach; ?>
                            <?php else: ?>
                                <span class="badge bg-secondary">No roles</span>
                            <?php endif; ?>
                        </td>
                        <td><?= htmlspecialchars($user->createdAt) ?></td>
                        <td>
                            <a href="/users/<?= $user->id ?>/edit" class="btn btn-sm btn-warning">Edit</a>
                            <form method="POST" action="/users/<?= $user->id ?>/delete" style="display: inline;">
                                <button type="submit" class="btn btn-sm btn-danger" onclick="return confirm('Are you sure you want to delete this user?')">Delete</button>
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
