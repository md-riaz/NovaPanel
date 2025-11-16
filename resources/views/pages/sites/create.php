<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Create Site</h1>
</div>

<div class="row">
    <div class="col-md-8">
        <form method="POST" action="/sites">
            <div class="mb-3">
                <label for="domain" class="form-label">Domain Name</label>
                <input type="text" class="form-control" id="domain" name="domain" required>
                <div class="form-text">Enter the domain name (e.g., example.com)</div>
            </div>

            <div class="mb-3">
                <label for="user" class="form-label">Site Owner (Panel User)</label>
                <select class="form-select" id="user" name="user_id" required>
                    <option value="">Select a panel user</option>
                    <?php foreach ($users ?? [] as $user): ?>
                        <option value="<?= $user->id ?>"><?= htmlspecialchars($user->username) ?> (<?= htmlspecialchars($user->email) ?>)</option>
                    <?php endforeach; ?>
                </select>
                <div class="form-text">The panel user who will own and manage this site</div>
            </div>

            <div class="mb-3">
                <label for="php_version" class="form-label">PHP Version</label>
                <select class="form-select" id="php_version" name="php_version">
                    <option value="8.2">PHP 8.2</option>
                    <option value="8.1">PHP 8.1</option>
                    <option value="8.0">PHP 8.0</option>
                    <option value="7.4">PHP 7.4</option>
                </select>
            </div>

            <div class="mb-3 form-check">
                <input type="checkbox" class="form-check-input" id="ssl_enabled" name="ssl_enabled">
                <label class="form-check-label" for="ssl_enabled">Enable SSL</label>
            </div>

            <div class="mb-3">
                <button type="submit" class="btn btn-primary">Create Site</button>
                <a href="/sites" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
