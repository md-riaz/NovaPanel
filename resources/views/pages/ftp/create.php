<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Create FTP User</h1>
</div>

<div class="row">
    <div class="col-md-8">
        <div id="alert-container"></div>
        
        <form method="POST" action="/ftp" 
              hx-post="/ftp" 
              hx-target="#alert-container"
              hx-on::after-request="if(event.detail.successful) { setTimeout(() => window.location.href='/ftp', 1500); }">
            
            <div class="mb-3">
                <label for="ftp_username" class="form-label">FTP Username</label>
                <input type="text" class="form-control" id="ftp_username" name="ftp_username" required>
                <div class="form-text">3-32 characters, alphanumeric with underscores and hyphens</div>
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
                <label for="password" class="form-label">Password</label>
                <input type="password" class="form-control" id="password" name="password" required minlength="8">
                <div class="form-text">Minimum 8 characters</div>
            </div>

            <div class="mb-3">
                <label for="home_directory" class="form-label">Home Directory</label>
                <input type="text" class="form-control" id="home_directory" name="home_directory" 
                       value="/opt/novapanel/sites/" required>
                <div class="form-text">Must be within /opt/novapanel/sites/</div>
            </div>

            <div class="mb-3">
                <button type="submit" class="btn btn-primary">
                    <span class="htmx-indicator spinner-border spinner-border-sm" role="status"></span>
                    Create FTP User
                </button>
                <a href="/ftp" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
