<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Create DNS Zone</h1>
</div>

<div class="row">
    <div class="col-md-8">
        <div id="alert-container"></div>
        
        <form method="POST" action="/dns" 
              hx-post="/dns" 
              hx-target="#alert-container"
              hx-on::after-request="if(event.detail.successful) { setTimeout(() => window.location.href='/dns', 1500); }">
            
            <div class="mb-3">
                <label for="site_id" class="form-label">Site</label>
                <select class="form-select" id="site_id" name="site_id" required>
                    <option value="">Select a site</option>
                    <?php foreach ($sites ?? [] as $site): ?>
                        <option value="<?= $site->id ?>"><?= htmlspecialchars($site->domain) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>

            <div class="mb-3">
                <label for="domain_name" class="form-label">Domain Name</label>
                <input type="text" class="form-control" id="domain_name" name="domain_name" 
                       placeholder="example.com" required>
                <div class="form-text">The domain name for this DNS zone</div>
            </div>

            <div class="mb-3">
                <label for="server_ip" class="form-label">Server IP Address (Optional)</label>
                <input type="text" class="form-control" id="server_ip" name="server_ip" 
                       placeholder="192.0.2.1">
                <div class="form-text">If provided, default A and CNAME records will be created</div>
            </div>

            <div class="mb-3">
                <button type="submit" class="btn btn-primary">
                    <span class="htmx-indicator spinner-border spinner-border-sm" role="status"></span>
                    Create DNS Zone
                </button>
                <a href="/dns" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
