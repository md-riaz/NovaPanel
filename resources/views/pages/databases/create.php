<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Create Database</h1>
</div>

<div class="row">
    <div class="col-md-8">
        <form method="POST" action="/databases">
            <div class="mb-3">
                <label for="name" class="form-label">Database Name</label>
                <input type="text" class="form-control" id="name" name="name" required>
                <div class="form-text">Alphanumeric and underscores only</div>
            </div>

            <div class="mb-3">
                <label for="account" class="form-label">Account</label>
                <select class="form-select" id="account" name="account_id" required>
                    <option value="">Select an account</option>
                </select>
            </div>

            <div class="mb-3">
                <label for="type" class="form-label">Database Type</label>
                <select class="form-select" id="type" name="type">
                    <option value="mysql">MySQL/MariaDB</option>
                    <option value="postgresql">PostgreSQL</option>
                </select>
            </div>

            <div class="mb-3">
                <button type="submit" class="btn btn-primary">Create Database</button>
                <a href="/databases" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
