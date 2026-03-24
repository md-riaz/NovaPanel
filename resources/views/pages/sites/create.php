<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Create Site</h1>
</div>

<div class="row g-4">
    <div class="col-md-8">
        <div id="form-messages"></div>
        <form method="POST" action="/sites"
              hx-post="/sites"
              hx-target="#form-messages"
              hx-swap="innerHTML"
              hx-indicator="#submit-indicator">
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
                <label for="template" class="form-label">Application Template</label>
                <select class="form-select" id="template" name="template">
                    <?php foreach ($templates ?? [] as $template): ?>
                        <option value="<?= htmlspecialchars($template['key']) ?>" <?= ($selectedTemplate ?? 'basic_php') === $template['key'] ? 'selected' : '' ?>>
                            <?= htmlspecialchars($template['name']) ?>
                        </option>
                    <?php endforeach; ?>
                </select>
                <div class="form-text">Choose a starter blueprint to reduce post-provisioning setup work.</div>
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
                <button type="submit" class="btn btn-primary">
                    <span class="htmx-indicator spinner-border spinner-border-sm" id="submit-indicator" role="status"></span>
                    Create Site
                </button>
                <a href="/sites" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>
    <div class="col-md-4">
        <div class="card border-0 shadow-sm h-100">
            <div class="card-body">
                <h5 class="card-title">Template guide</h5>
                <div class="list-group list-group-flush">
                    <?php foreach ($templates ?? [] as $template): ?>
                        <div class="list-group-item px-0">
                            <div class="fw-semibold"><?= htmlspecialchars($template['name']) ?></div>
                            <div class="small text-muted"><?= htmlspecialchars($template['summary']) ?></div>
                        </div>
                    <?php endforeach; ?>
                </div>
                <a href="/templates" class="btn btn-sm btn-outline-primary mt-3">Browse module details</a>
            </div>
        </div>
    </div>
</div>

<script>
document.body.addEventListener('htmx:afterRequest', function(evt) {
    if (evt.detail.successful && evt.detail.target.id === 'form-messages') {
        setTimeout(() => {
            window.location.href = '/sites';
        }, 1000);
    }
});
</script>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
