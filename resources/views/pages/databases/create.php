<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Create Database</h1>
</div>

<div class="row">
    <div class="col-md-8">
        <div id="alert-container"></div>
        
        <form method="POST" action="/databases" 
              hx-post="/databases" 
              hx-target="#alert-container"
              hx-on::after-request="if(event.detail.successful) { setTimeout(() => window.location.href='/databases', 1500); }">
            
            <div class="mb-3">
                <label for="db_name" class="form-label">Database Name</label>
                <input type="text" class="form-control" id="db_name" name="db_name" required>
                <div class="form-text">Alphanumeric and underscores only, max 64 characters</div>
            </div>

            <div class="mb-3">
                <label for="user_id" class="form-label">Panel User (Owner)</label>
                <select class="form-select" id="user_id" name="user_id" required>
                    <option value="">Select a panel user</option>
                    <?php foreach ($users ?? [] as $user): ?>
                        <option value="<?= $user->id ?>"><?= htmlspecialchars($user->username) ?> (<?= htmlspecialchars($user->email) ?>)</option>
                    <?php endforeach; ?>
                </select>
            </div>

            <div class="mb-3">
                <label for="db_type" class="form-label">Database Type</label>
                <select class="form-select" id="db_type" name="db_type">
                    <option value="mysql">MySQL/MariaDB</option>
                </select>
                <div class="form-text">Only MySQL is supported in the default installation</div>
            </div>

            <h5 class="mt-4">Database User (Optional)</h5>
            <p class="text-muted">Create a database user with access to this database</p>

            <div class="mb-3">
                <label for="db_username" class="form-label">Database Username</label>
                <input type="text" class="form-control" id="db_username" name="db_username">
                <div class="form-text">Leave empty to skip user creation</div>
            </div>

            <div class="mb-3">
                <label for="db_password" class="form-label">Database Password</label>
                <input type="password" class="form-control" id="db_password" name="db_password">
            </div>

            <div class="mb-3">
                <button type="submit" class="btn btn-primary">
                    <span class="htmx-indicator spinner-border spinner-border-sm" role="status"></span>
                    Create Database
                </button>
                <a href="/databases" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
