<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">DNS Records - <?= htmlspecialchars($domain->name ?? '') ?></h1>
    <a href="/dns" class="btn btn-sm btn-outline-secondary">
        <i class="bi bi-arrow-left"></i> Back to DNS Zones
    </a>
</div>

<div class="row mb-4">
    <div class="col-md-12">
        <div class="card">
            <div class="card-header">
                <h5 class="card-title mb-0">Add DNS Record</h5>
            </div>
            <div class="card-body">
                <form method="POST" action="/dns/<?= $domain->id ?? 0 ?>/records" class="row g-3">
                    <div class="col-md-2">
                        <label for="name" class="form-label">Name</label>
                        <input type="text" class="form-control" id="name" name="name" 
                               placeholder="@" required>
                    </div>
                    
                    <div class="col-md-2">
                        <label for="type" class="form-label">Type</label>
                        <select class="form-select" id="type" name="type" required>
                            <option value="A">A</option>
                            <option value="AAAA">AAAA</option>
                            <option value="CNAME">CNAME</option>
                            <option value="MX">MX</option>
                            <option value="TXT">TXT</option>
                            <option value="SRV">SRV</option>
                        </select>
                    </div>
                    
                    <div class="col-md-3">
                        <label for="content" class="form-label">Content</label>
                        <input type="text" class="form-control" id="content" name="content" 
                               placeholder="192.0.2.1" required>
                    </div>
                    
                    <div class="col-md-2">
                        <label for="ttl" class="form-label">TTL</label>
                        <input type="number" class="form-control" id="ttl" name="ttl" 
                               value="3600" required>
                    </div>
                    
                    <div class="col-md-2">
                        <label for="priority" class="form-label">Priority</label>
                        <input type="number" class="form-control" id="priority" name="priority" 
                               placeholder="10">
                    </div>
                    
                    <div class="col-md-1 d-flex align-items-end">
                        <button type="submit" class="btn btn-primary w-100">Add</button>
                    </div>
                </form>
            </div>
        </div>
    </div>
</div>

<div class="row">
    <div class="col-md-12">
        <div class="card">
            <div class="card-header">
                <h5 class="card-title mb-0">DNS Records</h5>
            </div>
            <div class="card-body">
                <div class="table-responsive">
                    <table class="table table-striped">
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
                            <?php if (empty($records)): ?>
                                <tr>
                                    <td colspan="6" class="text-center text-muted">
                                        No DNS records found. Add one above to get started.
                                    </td>
                                </tr>
                            <?php else: ?>
                                <?php foreach ($records as $record): ?>
                                    <tr>
                                        <td><?= htmlspecialchars($record->name) ?></td>
                                        <td><span class="badge bg-secondary"><?= htmlspecialchars($record->type) ?></span></td>
                                        <td><?= htmlspecialchars($record->content) ?></td>
                                        <td><?= $record->ttl ?></td>
                                        <td><?= $record->priority ?? '-' ?></td>
                                        <td>
                                            <form method="POST" action="/dns/<?= $domain->id ?>/records/<?= $record->id ?>/delete" 
                                                  style="display: inline;" 
                                                  onsubmit="return confirm('Are you sure you want to delete this DNS record?');">
                                                <button type="submit" class="btn btn-sm btn-danger">
                                                    <i class="bi bi-trash"></i> Delete
                                                </button>
                                            </form>
                                        </td>
                                    </tr>
                                <?php endforeach; ?>
                            <?php endif; ?>
                        </tbody>
                    </table>
                </div>
            </div>
        </div>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
