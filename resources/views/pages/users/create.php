<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Create Panel User</h1>
</div>

<div class="alert alert-info" role="alert">
    <i class="bi bi-info-circle"></i> 
    Panel users can log into the NovaPanel interface. Assign roles to control their permissions.
</div>

<div class="row">
    <div class="col-md-8">
        <div id="form-messages"></div>
        <form method="POST" action="/users"
              hx-post="/users"
              hx-target="#form-messages"
              hx-swap="innerHTML"
              hx-indicator="#submit-indicator">
            <div class="mb-3">
                <label for="username" class="form-label">Username</label>
                <input type="text" class="form-control" id="username" name="username" required>
                <div class="form-text">Username for logging into the panel</div>
            </div>

            <div class="mb-3">
                <label for="email" class="form-label">Email Address</label>
                <input type="email" class="form-control" id="email" name="email" required>
                <div class="form-text">Email address for the user</div>
            </div>

            <div class="mb-3">
                <label for="password" class="form-label">Password</label>
                <input type="password" class="form-control" id="password" name="password" required minlength="8">
                <div class="form-text">Password must be at least 8 characters long</div>
            </div>

            <div class="mb-3">
                <label for="password_confirm" class="form-label">Confirm Password</label>
                <input type="password" class="form-control" id="password_confirm" name="password_confirm" required minlength="8">
            </div>

            <div class="mb-3">
                <label class="form-label">Roles</label>
                <?php foreach ($roles ?? [] as $role): ?>
                    <div class="form-check">
                        <input class="form-check-input" type="checkbox" name="roles[]" value="<?= $role->id ?>" id="role_<?= $role->id ?>">
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
                <button type="submit" class="btn btn-primary">
                    <span class="htmx-indicator spinner-border spinner-border-sm" id="submit-indicator" role="status"></span>
                    Create Panel User
                </button>
                <a href="/users" class="btn btn-secondary">Cancel</a>
            </div>
        </form>

        <script>
        document.body.addEventListener('htmx:afterRequest', function(evt) {
            if (evt.detail.successful && evt.detail.target.id === 'form-messages' && evt.detail.pathInfo.requestPath === '/users') {
                // Redirect to users page on success
                setTimeout(() => {
                    window.location.href = '/users';
                }, 1000);
            }
        });
        </script>
    </div>
    
    <div class="col-md-4">
        <div class="card">
            <div class="card-header">
                <strong>Role Descriptions</strong>
            </div>
            <div class="card-body">
                <dl>
                    <dt>Admin</dt>
                    <dd>Full system access - can manage all users, accounts, and sites</dd>
                    
                    <dt>AccountOwner</dt>
                    <dd>Can manage their own accounts and sites</dd>
                    
                    <dt>Developer</dt>
                    <dd>Limited access to manage sites and databases</dd>
                    
                    <dt>ReadOnly</dt>
                    <dd>Can only view information, no modification allowed</dd>
                </dl>
            </div>
        </div>
    </div>
</div>

<script>
document.querySelector('form').addEventListener('submit', function(e) {
    const password = document.getElementById('password').value;
    const confirm = document.getElementById('password_confirm').value;
    
    if (password !== confirm) {
        e.preventDefault();
        alert('Passwords do not match!');
    }
});
</script>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
