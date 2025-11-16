<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Dashboard</h1>
</div>

<div class="row">
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-primary">
            <div class="card-body">
                <h5 class="card-title">Accounts</h5>
                <p class="card-text display-4">0</p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-success">
            <div class="card-body">
                <h5 class="card-title">Sites</h5>
                <p class="card-text display-4">0</p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-info">
            <div class="card-body">
                <h5 class="card-title">Databases</h5>
                <p class="card-text display-4">0</p>
            </div>
        </div>
    </div>
    <div class="col-md-3 mb-3">
        <div class="card text-white bg-warning">
            <div class="card-body">
                <h5 class="card-title">FTP Users</h5>
                <p class="card-text display-4">0</p>
            </div>
        </div>
    </div>
</div>

<div class="row mt-4">
    <div class="col-md-12">
        <h3>Quick Actions</h3>
        <div class="list-group">
            <a href="/accounts/create" class="list-group-item list-group-item-action">
                <i class="bi bi-plus-circle"></i> Create New Account
            </a>
            <a href="/sites/create" class="list-group-item list-group-item-action">
                <i class="bi bi-plus-circle"></i> Create New Site
            </a>
            <a href="/databases/create" class="list-group-item list-group-item-action">
                <i class="bi bi-plus-circle"></i> Create New Database
            </a>
        </div>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../layouts/app.php'; ?>
