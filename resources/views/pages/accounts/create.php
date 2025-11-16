<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Create Account</h1>
</div>

<div class="row">
    <div class="col-md-8">
        <form method="POST" action="/accounts">
            <div class="mb-3">
                <label for="username" class="form-label">Username</label>
                <input type="text" class="form-control" id="username" name="username" required>
                <div class="form-text">System username for this account</div>
            </div>

            <div class="mb-3">
                <label for="user_id" class="form-label">Panel User</label>
                <select class="form-select" id="user_id" name="user_id" required>
                    <option value="">Select a user</option>
                </select>
            </div>

            <div class="mb-3">
                <label for="home_directory" class="form-label">Home Directory</label>
                <input type="text" class="form-control" id="home_directory" name="home_directory">
                <div class="form-text">Leave empty to use default (/home/username)</div>
            </div>

            <div class="mb-3">
                <button type="submit" class="btn btn-primary">Create Account</button>
                <a href="/accounts" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
