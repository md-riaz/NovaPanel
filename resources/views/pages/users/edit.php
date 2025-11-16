<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Edit Panel User</h1>
</div>

<div class="row">
    <div class="col-md-8">
        <form method="POST" action="/users/<?= $user->id ?>">
            <input type="hidden" name="_method" value="PUT">
            
            <div class="mb-3">
                <label for="username" class="form-label">Username</label>
                <input type="text" class="form-control" id="username" name="username" value="<?= htmlspecialchars($user->username) ?>" required>
                <div class="form-text">Username for logging into the panel</div>
            </div>

            <div class="mb-3">
                <label for="email" class="form-label">Email Address</label>
                <input type="email" class="form-control" id="email" name="email" value="<?= htmlspecialchars($user->email) ?>" required>
                <div class="form-text">Email address for the user</div>
            </div>

            <div class="mb-3">
                <label for="password" class="form-label">New Password</label>
                <input type="password" class="form-control" id="password" name="password" minlength="8">
                <div class="form-text">Leave blank to keep current password. Must be at least 8 characters if changing.</div>
            </div>

            <div class="mb-3">
                <label for="password_confirm" class="form-label">Confirm New Password</label>
                <input type="password" class="form-control" id="password_confirm" name="password_confirm" minlength="8">
            </div>

            <div class="mb-3">
                <label class="form-label">Roles</label>
                <?php 
                $userRoleIds = array_map(fn($role) => $role->id, $user->roles ?? []);
                ?>
                <?php foreach ($roles ?? [] as $role): ?>
                    <div class="form-check">
                        <input class="form-check-input" type="checkbox" name="roles[]" value="<?= $role->id ?>" id="role_<?= $role->id ?>" 
                            <?= in_array($role->id, $userRoleIds) ? 'checked' : '' ?>>
                        <label class="form-check-label" for="role_<?= $role->id ?>">
                            <strong><?= htmlspecialchars($role->name) ?></strong>
                            <?php if ($role->description): ?>
                                - <?= htmlspecialchars($role->description) ?>
                            <?php endif; ?>
                        </label>
                    </div>
                <?php endforeach; ?>
            </div>

            <div class="mb-3">
                <button type="submit" class="btn btn-primary">Update Panel User</button>
                <a href="/users" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>
    
    <div class="col-md-4">
        <div class="card">
            <div class="card-header">
                <strong>User Information</strong>
            </div>
            <div class="card-body">
                <dl>
                    <dt>User ID</dt>
                    <dd><?= $user->id ?></dd>
                    
                    <dt>Created At</dt>
                    <dd><?= htmlspecialchars($user->createdAt) ?></dd>
                    
                    <dt>Last Updated</dt>
                    <dd><?= htmlspecialchars($user->updatedAt) ?></dd>
                </dl>
            </div>
        </div>
    </div>
</div>

<script>
document.querySelector('form').addEventListener('submit', function(e) {
    const password = document.getElementById('password').value;
    const confirm = document.getElementById('password_confirm').value;
    
    if (password && password !== confirm) {
        e.preventDefault();
        alert('Passwords do not match!');
    }
});
</script>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
