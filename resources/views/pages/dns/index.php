<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">DNS Management</h1>
    <a href="/dns/create" class="btn btn-primary">
        <i class="bi bi-plus-circle"></i> Add DNS Record
    </a>
</div>

<div class="mb-3">
    <label for="domain-select" class="form-label">Select Domain</label>
    <select class="form-select" id="domain-select">
        <option value="">-- Select a domain --</option>
    </select>
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover">
        <thead>
            <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Content</th>
                <th>TTL</th>
                <th>Priority</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            <tr>
                <td colspan="6" class="text-center text-muted">Select a domain to view DNS records</td>
            </tr>
        </tbody>
    </table>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
